use crate::admission::{Execution, Limiter, stopped};
use crate::backend::{Backend, bounded_json};
use crate::configuration::{Configuration, Limits, Result};
use crate::control::{AdmissionRequest, Control};
use crate::errors::{self, Failure};
use crate::messages::{self, MessagesStream};
use crate::protocol::{ChatRequest, normalize_completion};
use crate::quota::{Quotas, RequestPermit, shared};
use crate::responses::{ResponseIdentity, ResponsesStream};
use crate::storage::Spool;
use crate::streaming::{Decoder, Projection};
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
    quotas: Option<Arc<Quotas>>,
    quota_authority: Option<Arc<shared::Authority>>,
    quota_client: Option<Arc<shared::Client>>,
    shutdown: watch::Sender<bool>,
    workers: Mutex<JoinSet<()>>,
}

impl Application {
    pub fn new(configuration: Configuration) -> Result<Arc<Self>> {
        Self::with_quota_clock(configuration, || {
            time::OffsetDateTime::now_utc().unix_timestamp()
        })
    }

    pub(crate) fn with_quota_clock(
        configuration: Configuration,
        clock: fn() -> i64,
    ) -> Result<Arc<Self>> {
        let control = Control::new(&configuration)?;
        let backend = Backend::new(&configuration)?;
        let limiter = Limiter::new(&configuration.limits)?;
        let spool = Spool::open(&configuration)?;
        shared::verify_role(&configuration)?;
        let quotas = Quotas::open(&configuration, &spool, clock)?.map(Arc::new);
        let quota_authority = shared::Authority::open(&configuration, quotas.as_ref())?;
        let quota_client = shared::Client::open(&configuration)?;
        let (shutdown, _) = watch::channel(false);
        Ok(Arc::new(Self {
            configuration,
            control,
            backend,
            spool,
            limiter,
            quotas,
            quota_authority,
            quota_client,
            shutdown,
            workers: Mutex::new(JoinSet::new()),
        }))
    }
    async fn quota_permit(
        &self,
        workspace: Uuid,
        key: &str,
    ) -> std::result::Result<Option<RequestPermit>, Failure> {
        if let Some(client) = &self.quota_client {
            return client
                .acquire(workspace, key)
                .await
                .map(RequestPermit::Shared)
                .map(Some);
        }
        if let Some(authority) = &self.quota_authority {
            authority.prune()?;
        }
        self.quotas
            .as_ref()
            .map(|quotas| quotas.acquire(workspace, key).map(RequestPermit::Local))
            .transpose()
    }

    pub fn receiver(&self) -> watch::Receiver<bool> {
        self.shutdown.subscribe()
    }
    pub fn shutdown(&self) {
        if let Some(authority) = &self.quota_authority {
            authority.stop();
        }
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
    let mut router = Router::new()
        .route("/v1/models", get(models))
        .route("/v1/chat/completions", post(chat))
        .route("/v1/responses", post(responses))
        .route("/v1/messages", post(create_message))
        .route(
            "/health/live",
            get(|| async { Json(json!({"status":"alive"})) }),
        )
        .route("/health/ready", get(ready))
        .fallback(|| async { errors::response("not_found", Uuid::new_v4()) })
        .method_not_allowed_fallback(|| async {
            errors::response("method_not_allowed", Uuid::new_v4())
        });
    if let Some(authority) = &application.quota_authority {
        router = router.merge(authority.clone().router());
    }
    router.with_state(application)
}

async fn ready(State(app): State<Arc<Application>>) -> Response {
    if app.is_stopped()
        || !app.spool.ready()
        || app
            .quota_authority
            .as_ref()
            .is_some_and(|authority| !authority.ready())
    {
        return errors::response("gateway_unavailable", Uuid::new_v4());
    }
    let mut response = Json(json!({"status":"locally_ready"})).into_response();
    errors::protect(&mut response, Uuid::new_v4());
    response
}

fn metadata(request: &Request, post: bool, format: Format) -> Result<String> {
    let headers = request.headers();
    let messages = matches!(format, Format::Messages);
    if request.uri().query().is_some()
        || headers.get_all(header::AUTHORIZATION).iter().count() > 1
        || headers.get_all("x-api-key").iter().count() > usize::from(messages)
        || (headers.contains_key("x-api-key") && headers.contains_key(header::AUTHORIZATION))
        || [
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
    if messages {
        messages::validate_headers(headers)?;
    }
    let key = match headers.get("x-api-key") {
        Some(value) => value.to_str().ok(),
        None => headers
            .get(header::AUTHORIZATION)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.strip_prefix("Bearer ")),
    }
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

pub(crate) async fn bounded<T, E: From<&'static str>>(
    shutdown: watch::Receiver<bool>,
    deadline: Instant,
    future: impl Future<Output = std::result::Result<T, E>>,
) -> std::result::Result<T, E> {
    if deadline <= Instant::now() {
        return Err("upstream_timeout".into());
    }
    tokio::select! {
        biased;
        _=stopped(shutdown)=>Err("gateway_shutdown".into()),
        _=tokio::time::sleep_until(deadline)=>Err("upstream_timeout".into()),
        result=future=>result,
    }
}

async fn models(State(app): State<Arc<Application>>, request: Request) -> Response {
    let id = Uuid::new_v4();
    let result = async move {
        if app.is_stopped() {
            return Err(Failure::from("gateway_shutdown"));
        }
        let _ingress = app.limiter.enter()?;
        let key = metadata(&request, false, Format::Chat)?;
        let authorization = app.control.models_with_retry(&key).await?;
        let quota = app.quota_permit(authorization.workspace_id, &key).await?;
        let Some(first) = authorization.models.first() else {
            return Ok(json!({"object":"list","data":[]}));
        };
        if authorization.models.iter().any(|binding| binding.backend_model != first.backend_model) {
            return Err("gateway_unavailable".into());
        }
        let limits = &app.configuration.limits;
        let maximum = Instant::now() + Duration::from_secs(limits.request_seconds);
        let deadline = quota.as_ref().map_or(maximum, |permit| permit.deadline(maximum));
        bounded(
            app.receiver(),
            deadline,
            app.backend.verify_model(&first.backend_model, limits),
        ).await?;
        let models = authorization.models.iter().map(|binding| {
            json!({"id":binding.name,"object":"model","created":binding.created,"owned_by":"workspace"})
        }).collect::<Vec<_>>();
        Ok(json!({"object":"list","data":models}))
    }.await;
    match result {
        Ok(value) => {
            let mut response = Json(value).into_response();
            errors::protect(&mut response, id);
            response
        }
        Err(error) => errors::respond(error, id),
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

#[derive(Clone, Copy)]
enum Format {
    Chat,
    Responses,
    Messages,
}

impl Format {
    fn failure(self, failure: impl Into<Failure>, id: Uuid) -> Response {
        match self {
            Self::Chat | Self::Responses => errors::respond(failure.into(), id),
            Self::Messages => errors::respond_messages(failure.into(), id),
        }
    }

    fn protect(self, response: &mut Response, id: Uuid) {
        errors::protect(response, id);
        if matches!(self, Self::Messages) {
            errors::protect_messages(response, id);
        }
    }
}

async fn accept(
    app: &Application,
    request: Request,
    id: Uuid,
    format: Format,
) -> std::result::Result<Accepted, Failure> {
    if app.is_stopped() {
        return Err("gateway_shutdown".into());
    }
    let ingress = app.limiter.enter()?;
    if !app.spool.ready() {
        return Err("usage_storage_unavailable".into());
    }
    let key = metadata(&request, true, format)?;
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
    let limits = &app.configuration.limits;
    let request = match format {
        Format::Chat => ChatRequest::parse(&body, limits)?,
        Format::Responses => ChatRequest::parse_responses(&body, limits)?,
        Format::Messages => messages::parse(&body, limits)?,
    };
    let authorization = app.control.models_with_retry(&key).await?;
    let Some(alias_id) = authorization
        .models
        .iter()
        .find(|binding| binding.name == request.alias)
        .map(|binding| binding.id)
    else {
        return Err("model_not_found".into());
    };
    let quota = app.quota_permit(authorization.workspace_id, &key).await?;
    let active = app.limiter.active(app.receiver()).await?;
    let reservation = app.spool.reserve(id)?;
    // Authorization is repeated after the queue, immediately before any backend operation.
    let admission = app
        .control
        .admit_with_retry(&AdmissionRequest {
            request_id: id,
            api_key: &key,
            model: &request.alias,
            maximum_output_tokens: request.maximum_tokens,
            stream: request.stream,
        })
        .await?;
    let mut execution = Execution::new(reservation, id);
    if admission.workspace_id != authorization.workspace_id || admission.binding.id != alias_id {
        execution.refused_before_generation()?;
        return Err("gateway_unavailable".into());
    }
    let mut limits = app.configuration.limits.clone();
    limits.maximum_context_tokens = limits
        .maximum_context_tokens
        .min(admission.maximum_context_tokens);
    limits.request_seconds = limits
        .request_seconds
        .min(admission.maximum_request_seconds);
    let maximum = Instant::now() + Duration::from_secs(limits.request_seconds);
    let deadline = quota
        .as_ref()
        .map_or(maximum, |permit| permit.deadline(maximum));
    execution.attach_quota(quota);
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
    generate(app, request, Format::Chat).await
}

async fn responses(State(app): State<Arc<Application>>, request: Request) -> Response {
    generate(app, request, Format::Responses).await
}

async fn create_message(State(app): State<Arc<Application>>, request: Request) -> Response {
    generate(app, request, Format::Messages).await
}

async fn generate(app: Arc<Application>, request: Request, format: Format) -> Response {
    let id = Uuid::new_v4();
    let mut accepted = match accept(&app, request, id, format).await {
        Ok(value) => value,
        Err(error) => return format.failure(error, id),
    };
    let responses_identity =
        matches!(format, Format::Responses).then(|| ResponseIdentity::new(id, &accepted.request));
    let prompt = match bounded(
        app.receiver(),
        accepted.deadline,
        app.backend
            .tokenize(&accepted.request, &accepted.backend_model, &accepted.limits),
    )
    .await
    {
        Ok(prompt) => prompt,
        Err(code) => {
            let code = accepted.execution.failure(code, None).err().unwrap_or(code);
            return format.failure(code, id);
        }
    };
    let reserved = bounded(
        app.receiver(),
        accepted.deadline,
        accepted
            .execution
            .reserve_tokens(prompt + accepted.request.maximum_tokens),
    )
    .await;
    if let Err(error) = reserved {
        return match accepted.execution.refused_before_generation() {
            Ok(()) => format.failure(error, id),
            Err(code) => format.failure(code, id),
        };
    }
    let response = match bounded(
        app.receiver(),
        accepted.deadline,
        app.backend
            .completion(&accepted.request, &accepted.backend_model, id),
    )
    .await
    {
        Ok(response) => response,
        Err(code) => {
            let code = accepted.execution.failure(code, None).err().unwrap_or(code);
            return format.failure(code, id);
        }
    };
    if !accepted.request.stream {
        let result = bounded(app.receiver(), accepted.deadline, async {
            let bytes = bounded_json(response, accepted.limits.response_bytes).await?;
            let completion = normalize_completion(
                &bytes,
                &accepted.request,
                &accepted.backend_model,
                prompt,
                &accepted.limits,
            )?;
            let value = match (&responses_identity, format) {
                (Some(identity), _) => identity.complete(&completion),
                (None, Format::Messages) => {
                    messages::complete(id, &accepted.request.alias, &completion)?
                }
                (None, _) => completion.chat(&accepted.request.alias),
            };
            Ok((value, completion.usage))
        })
        .await;
        return match result {
            Ok((value, usage)) => {
                accepted.execution.settle_tokens(&usage).await;
                match accepted.execution.success(usage) {
                    Ok(()) => {
                        let mut response = Json(value).into_response();
                        format.protect(&mut response, id);
                        response
                    }
                    Err(code) => format.failure(code, id),
                }
            }
            Err(code) => {
                let code = accepted.execution.failure(code, None).err().unwrap_or(code);
                format.failure(code, id)
            }
        };
    }
    // One bounded event in transit; the worker has independent idle/total deadlines even for an unread body.
    let (sender, mut receiver) = mpsc::channel::<Vec<u8>>(1);
    let projection = match (responses_identity, format) {
        (Some(identity), _) => Projection::Responses(ResponsesStream::new(
            identity,
            accepted.limits.response_bytes as usize,
        )),
        (None, Format::Messages) => {
            Projection::Messages(MessagesStream::new(id, &accepted.request.alias, prompt))
        }
        (None, _) => Projection::Chat,
    };
    let decoder = Decoder::new(
        &accepted.request,
        &accepted.backend_model,
        prompt,
        &accepted.limits,
    )
    .with_projection(projection);
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
    format.protect(&mut response, id);
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
            assert_eq!(
                metadata(&request, true, Format::Chat),
                Err("invalid_request_error")
            );
        }
        let request = axum::http::Request::builder()
            .header(header::AUTHORIZATION, "Bearer synthetic-key")
            .header(header::AUTHORIZATION, "Bearer another-key")
            .body(Body::empty())
            .unwrap();
        assert_eq!(
            metadata(&request, false, Format::Chat),
            Err("invalid_request_error")
        );
        let request = axum::http::Request::builder()
            .uri("/v1/models?api_key=synthetic")
            .header(header::AUTHORIZATION, "Bearer synthetic-key")
            .body(Body::empty())
            .unwrap();
        assert_eq!(
            metadata(&request, false, Format::Chat),
            Err("invalid_request_error")
        );
    }
}
