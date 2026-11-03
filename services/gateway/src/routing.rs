use crate::admission::{Execution, Limiter, stopped};
use crate::backend::{Backend, bounded_json};
use crate::configuration::{Configuration, Limits, Result};
use crate::control::{AdmissionRequest, Control};
use crate::errors;
use crate::protocol::{ChatRequest, normalize_completion};
use crate::storage::Spool;
use axum::body::Body;
use axum::extract::{Request, State};
use axum::http::{StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::{get, post};
use axum::{Json, Router};
use serde_json::json;
use std::future::Future;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::{Mutex, OwnedSemaphorePermit, mpsc, watch};
use tokio::task::JoinSet;
use tokio::time::Instant;
use uuid::Uuid;

const MAX_API_KEY_BYTES: usize = 256;

pub struct Application {
    pub configuration: Configuration,
    pub control: Control,
    pub backend: Backend,
    pub spool: Arc<Spool>,
    limiter: Limiter,
    shutdown: watch::Sender<bool>,
    workers: Mutex<JoinSet<()>>,
}

impl Application {
    pub fn new(configuration: Configuration) -> Result<Arc<Self>> {
        let control = Control::new(&configuration)?;
        let backend = Backend::new(&configuration)?;
        let limiter = Limiter::new(&configuration.limits)?;
        let spool = Spool::open(&configuration)?;
        let (shutdown, _) = watch::channel(false);
        Ok(Arc::new(Self {
            configuration,
            control,
            backend,
            spool,
            limiter,
            shutdown,
            workers: Mutex::new(JoinSet::new()),
        }))
    }
    pub fn receiver(&self) -> watch::Receiver<bool> {
        self.shutdown.subscribe()
    }
    pub fn shutdown(&self) {
        self.shutdown.send_replace(true);
    }
    pub fn is_stopped(&self) -> bool {
        *self.shutdown.borrow()
    }
    pub async fn spawn_stream(&self, work: crate::transport::StreamWork) {
        let mut workers = self.workers.lock().await;
        while workers.try_join_next().is_some() {}
        workers.spawn(work.run());
    }
    pub async fn finish_workers(&self) {
        let mut workers = self.workers.lock().await;
        let drained = tokio::time::timeout(
            Duration::from_secs(self.configuration.limits.shutdown_seconds),
            async { while workers.join_next().await.is_some() {} },
        )
        .await;
        if drained.is_err() {
            workers.abort_all();
            while workers.join_next().await.is_some() {}
        }
    }
}

pub fn router(application: Arc<Application>) -> Router {
    Router::new()
        .route("/v1/models", get(models))
        .route("/v1/chat/completions", post(chat))
        .route(
            "/health/live",
            get(|| async { Json(json!({"status":"alive"})) }),
        )
        .route("/health/ready", get(ready))
        .fallback(|| async { errors::response("not_found", Uuid::new_v4()) })
        .method_not_allowed_fallback(|| async {
            errors::response("method_not_allowed", Uuid::new_v4())
        })
        .with_state(application)
}

async fn ready(State(app): State<Arc<Application>>) -> Response {
    if app.is_stopped() || !app.spool.ready() {
        return errors::response("gateway_unavailable", Uuid::new_v4());
    }
    let mut response = Json(json!({"status":"locally_ready"})).into_response();
    errors::protect(&mut response, Uuid::new_v4());
    response
}

fn metadata(request: &Request, post: bool) -> Result<String> {
    let headers = request.headers();
    if request.uri().query().is_some()
        || headers.get_all(header::AUTHORIZATION).iter().count() > 1
        || [
            "x-api-key",
            "api-key",
            "proxy-authorization",
            "cookie",
            "content-encoding",
            "expect",
            "origin",
        ]
        .iter()
        .any(|key| headers.contains_key(*key))
        || headers.get_all(header::CONTENT_LENGTH).iter().count() > 1
    {
        return Err("invalid_request_error");
    }
    let key = headers
        .get(header::AUTHORIZATION)
        .and_then(|value| value.to_str().ok())
        .and_then(|value| value.strip_prefix("Bearer "))
        .ok_or("invalid_api_key")?;
    if key.is_empty()
        || key.len() > MAX_API_KEY_BYTES
        || !key
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || b"-_.".contains(&value))
    {
        return Err("invalid_api_key");
    }
    if post {
        if headers.get_all(header::CONTENT_TYPE).iter().count() != 1
            || headers
                .get(header::CONTENT_TYPE)
                .and_then(|value| value.to_str().ok())
                .is_none_or(|value| {
                    !matches!(
                        value,
                        "application/json" | "application/json; charset=utf-8"
                    )
                })
        {
            return Err("invalid_request_error");
        }
    } else if headers.contains_key(header::TRANSFER_ENCODING)
        || headers
            .get(header::CONTENT_LENGTH)
            .is_some_and(|value| value != "0")
    {
        return Err("invalid_request_error");
    }
    Ok(key.to_owned())
}

pub(crate) async fn bounded<T>(
    shutdown: watch::Receiver<bool>,
    deadline: Instant,
    future: impl Future<Output = Result<T>>,
) -> Result<T> {
    tokio::select! {
        biased;
        _=stopped(shutdown)=>Err("gateway_shutdown"),
        result=tokio::time::timeout_at(deadline,future)=>result.map_err(|_|"upstream_timeout")?,
    }
}

async fn models(State(app): State<Arc<Application>>, request: Request) -> Response {
    let id = Uuid::new_v4();
    let result=async move {
        if app.is_stopped(){return Err("gateway_shutdown");}
        let _ingress=app.limiter.enter()?;
        let key=metadata(&request,false)?;
        let authorization=app.control.models(&key).await?;
        let Some(first)=authorization.models.first()else{return Ok(json!({"object":"list","data":[]}));};
        if authorization.models.iter().any(|binding|binding.backend_model!=first.backend_model){return Err("gateway_unavailable");}
        bounded(app.receiver(),Instant::now()+Duration::from_secs(app.configuration.limits.request_seconds),app.backend.verify_model(&first.backend_model,&app.configuration.limits)).await?;
        Ok(json!({"object":"list","data":authorization.models.iter().map(|binding|json!({"id":binding.name,"object":"model","created":binding.created,"owned_by":"workspace"})).collect::<Vec<_>>()}))
    }.await;
    match result {
        Ok(value) => {
            let mut response = Json(value).into_response();
            errors::protect(&mut response, id);
            response
        }
        Err(code) => errors::response(code, id),
    }
}

struct Accepted {
    request: ChatRequest,
    backend_model: String,
    limits: Limits,
    deadline: Instant,
    execution: Execution,
    ingress: OwnedSemaphorePermit,
    active: OwnedSemaphorePermit,
}

async fn accept(app: &Application, request: Request, id: Uuid) -> Result<Accepted> {
    if app.is_stopped() {
        return Err("gateway_shutdown");
    }
    let ingress = app.limiter.enter()?;
    if !app.spool.ready() {
        return Err("usage_storage_unavailable");
    }
    let key = metadata(&request, true)?;
    let body = tokio::time::timeout(
        Duration::from_secs(app.configuration.limits.idle_seconds),
        axum::body::to_bytes(
            request.into_body(),
            app.configuration.limits.request_body_bytes as usize,
        ),
    )
    .await
    .map_err(|_| "invalid_request_error")?
    .map_err(|_| "request_too_large")?;
    let request = ChatRequest::parse(&body, &app.configuration.limits)?;
    let authorization = app.control.models(&key).await?;
    if !authorization
        .models
        .iter()
        .any(|binding| binding.name == request.alias)
    {
        return Err("model_not_found");
    }
    let active = app.limiter.active(app.receiver()).await?;
    let reservation = app.spool.reserve(id)?;
    // Authorization is repeated after the queue, immediately before any backend operation.
    let admission = app
        .control
        .admit(&AdmissionRequest {
            request_id: id,
            api_key: &key,
            model: &request.alias,
            maximum_output_tokens: request.maximum_tokens,
            stream: request.stream,
        })
        .await?;
    let execution = Execution::new(reservation, id);
    let mut limits = app.configuration.limits.clone();
    limits.maximum_context_tokens = limits
        .maximum_context_tokens
        .min(admission.maximum_context_tokens);
    limits.request_seconds = limits
        .request_seconds
        .min(admission.maximum_request_seconds);
    let deadline = Instant::now() + Duration::from_secs(limits.request_seconds);
    Ok(Accepted {
        request,
        backend_model: admission.binding.backend_model,
        limits,
        deadline,
        execution,
        ingress,
        active,
    })
}

async fn chat(State(app): State<Arc<Application>>, request: Request) -> Response {
    let id = Uuid::new_v4();
    let mut accepted = match accept(&app, request, id).await {
        Ok(value) => value,
        Err(code) => return errors::response(code, id),
    };
    let prepared = bounded(app.receiver(), accepted.deadline, async {
        let prompt = app
            .backend
            .tokenize(&accepted.request, &accepted.backend_model, &accepted.limits)
            .await?;
        let response = app
            .backend
            .completion(&accepted.request, &accepted.backend_model)
            .await?;
        Ok((prompt, response))
    })
    .await;
    let (prompt, response) = match prepared {
        Ok(value) => value,
        Err(code) => {
            let code = accepted.execution.failure(code, None).err().unwrap_or(code);
            return errors::response(code, id);
        }
    };
    if !accepted.request.stream {
        let result = bounded(app.receiver(), accepted.deadline, async {
            let bytes = bounded_json(response, accepted.limits.response_bytes).await?;
            normalize_completion(
                &bytes,
                &accepted.request,
                &accepted.backend_model,
                prompt,
                &accepted.limits,
            )
        })
        .await;
        return match result {
            Ok((value, usage)) => match accepted.execution.success(usage) {
                Ok(()) => {
                    let mut response = Json(value).into_response();
                    errors::protect(&mut response, id);
                    response
                }
                Err(code) => errors::response(code, id),
            },
            Err(code) => {
                let code = accepted.execution.failure(code, None).err().unwrap_or(code);
                errors::response(code, id)
            }
        };
    }
    // One bounded event in transit; the worker has independent idle/total deadlines even for an unread body.
    let (sender, mut receiver) = mpsc::channel::<Vec<u8>>(1);
    let decoder = crate::streaming::Decoder::new(
        &accepted.request,
        &accepted.backend_model,
        prompt,
        &accepted.limits,
    );
    let work = crate::transport::StreamWork {
        response,
        decoder,
        execution: accepted.execution,
        sender,
        shutdown: app.receiver(),
        deadline: accepted.deadline,
        idle: Duration::from_secs(accepted.limits.idle_seconds),
        request_id: id,
        ingress: accepted.ingress,
        active: accepted.active,
    };
    app.spawn_stream(work).await;
    let body = Body::from_stream(async_stream::stream! {
        while let Some(bytes)=receiver.recv().await {yield Ok::<_,std::convert::Infallible>(bytes);}
    });
    let mut response = (
        StatusCode::OK,
        [
            (header::CONTENT_TYPE, "text/event-stream"),
            (header::CONNECTION, "close"),
        ],
        body,
    )
        .into_response();
    errors::protect(&mut response, id);
    response
}

#[cfg(test)]
mod tests {
    use super::*;
    use axum::http::Method;
    #[test]
    fn metadata_rejects_conflicting_authority_query_cookie_and_missing_json_type() {
        for name in [
            "cookie",
            "x-api-key",
            "proxy-authorization",
            "origin",
            "content-encoding",
        ] {
            let request = axum::http::Request::builder()
                .method(Method::POST)
                .uri("/v1/chat/completions")
                .header(header::AUTHORIZATION, "Bearer synthetic-key")
                .header(header::CONTENT_TYPE, "application/json")
                .header(name, "synthetic")
                .body(Body::empty())
                .unwrap();
            assert_eq!(metadata(&request, true), Err("invalid_request_error"));
        }
        let request = axum::http::Request::builder()
            .header(header::AUTHORIZATION, "Bearer synthetic-key")
            .header(header::AUTHORIZATION, "Bearer another-key")
            .body(Body::empty())
            .unwrap();
        assert_eq!(metadata(&request, false), Err("invalid_request_error"));
        let request = axum::http::Request::builder()
            .uri("/v1/models?api_key=synthetic")
            .header(header::AUTHORIZATION, "Bearer synthetic-key")
            .body(Body::empty())
            .unwrap();
        assert_eq!(metadata(&request, false), Err("invalid_request_error"));
    }
}
