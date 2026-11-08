//! Actual loopback HTTP transport against explicitly synthetic vLLM-shaped responses.
use crate::backend::{Backend, bounded_json};
use crate::configuration::{Configuration, Limits};
use crate::protocol::{ChatRequest, normalize_completion};
use axum::body::Body;
use axum::extract::{Request, State};
use axum::http::{StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::{Json, Router};
use serde_json::json;
use std::fs;
use std::net::SocketAddr;
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;
use uuid::Uuid;

#[derive(Clone, Default)]
struct Synthetic {
    mode: Arc<AtomicUsize>,
    calls: Arc<AtomicUsize>,
}

async fn upstream(State(state): State<Synthetic>, request: Request) -> Response {
    state.calls.fetch_add(1, Ordering::SeqCst);
    assert!(!request.headers().contains_key(header::AUTHORIZATION));
    assert!(!request.headers().contains_key(header::COOKIE));
    match state.mode.load(Ordering::SeqCst) {
        1 => {
            return (
                StatusCode::TEMPORARY_REDIRECT,
                [(header::LOCATION, "/tokenize")],
            )
                .into_response();
        }
        2 => {
            return (
                [(header::CONTENT_TYPE, "application/json")],
                "x".repeat(513),
            )
                .into_response();
        }
        3 => {
            return (
                [(header::CONTENT_TYPE, "text/html")],
                "private synthetic diagnostic",
            )
                .into_response();
        }
        4 => tokio::time::sleep(Duration::from_secs(2)).await,
        6 => {
            let stream = async_stream::stream! {
                for _ in 0..3 {yield Ok::<_, std::io::Error>(vec![b'x';256]);}
            };
            return (
                [(header::CONTENT_TYPE, "application/json")],
                Body::from_stream(stream),
            )
                .into_response();
        }
        7 => {
            return (
                StatusCode::INTERNAL_SERVER_ERROR,
                "private synthetic diagnostic",
            )
                .into_response();
        }
        _ => {}
    }
    let path = request.uri().path().to_owned();
    if path == "/v1/models" {
        let model = if state.mode.load(Ordering::SeqCst) == 5 {
            "unexpected-model"
        } else {
            "synthetic-internal"
        };
        return Json(json!({"object":"list","data":[{"object":"model","id":model}]}))
            .into_response();
    }
    let bytes = axum::body::to_bytes(request.into_body(), 65_536)
        .await
        .unwrap();
    let body: serde_json::Value = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(body["model"], "synthetic-internal");
    assert_eq!(body["chat_template_kwargs"]["enable_thinking"], false);
    assert_eq!(
        body["messages"][0]["content"],
        "Synthetic request \u{4e2d}\u{6587}"
    );
    assert!(body.get("api_key").is_none());
    match path.as_str() {
        "/tokenize" => {
            assert_eq!(body["add_generation_prompt"], true);
            Json(json!({"count":7,"tokens":[1,2,3,4,5,6,7],"max_model_len":2048})).into_response()
        }
        "/v1/chat/completions" => {
            assert_eq!(body["max_tokens"], 16);
            Json(json!({"id":"chatcmpl-synthetic","object":"chat.completion","created":1,"model":"synthetic-internal","choices":[{"index":0,"message":{"role":"assistant","content":"Synthetic reply"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"private_diagnostic":"must never forward"})).into_response()
        }
        _ => StatusCode::NOT_FOUND.into_response(),
    }
}

struct Fixture {
    root: PathBuf,
    configuration: Configuration,
    state: Synthetic,
    server: tokio::task::JoinHandle<()>,
}
impl Fixture {
    async fn new() -> Self {
        let root = std::env::temp_dir().join(format!("gateway-backend-{}", Uuid::new_v4()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let root = root.canonicalize().unwrap();
        let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
        let SocketAddr::V4(address) = listener.local_addr().unwrap() else {
            panic!("IPv4 fixture required")
        };
        let state = Synthetic::default();
        let router = Router::new().fallback(upstream).with_state(state.clone());
        let server = tokio::spawn(async move { axum::serve(listener, router).await.unwrap() });
        let configuration = Configuration {
            shared_quota: None,
            quota_policy: None,
            gateway_id: Uuid::new_v4(),
            node_id: Uuid::new_v4(),
            listen_address: "127.0.0.1:19443".parse().unwrap(),
            backend_address: address,
            control_origin: "https://control.example.invalid".into(),
            site_id: "default".to_owned(),
            cell_id: "default".to_owned(),
            control_ca_file: root.join("ca.pem"),
            gateway_credential_file: root.join("credential"),
            tls_certificate_file: root.join("cert.pem"),
            tls_private_key_file: root.join("key.pem"),
            state_directory: root.clone(),
            limits: Limits {
                connect_seconds: 1,
                idle_seconds: 1,
                request_seconds: 3,
                response_bytes: 512,
                ..Limits::default()
            },
        };
        Self {
            root,
            configuration,
            state,
            server,
        }
    }
    fn request(&self) -> ChatRequest {
        ChatRequest::parse(&serde_json::to_vec(&json!({"model":"public-alias","messages":[{"role":"user","content":"Synthetic request \u{4e2d}\u{6587}"}],"max_tokens":16})).unwrap(),&self.configuration.limits).unwrap()
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        self.server.abort();
        fs::remove_dir_all(&self.root).unwrap();
    }
}

#[tokio::test]
async fn actual_http_models_tokenize_and_completion_use_fixed_destination_and_safe_payload() {
    let fixture = Fixture::new().await;
    let backend = Backend::new(&fixture.configuration).unwrap();
    let limits = &fixture.configuration.limits;
    let request = fixture.request();
    backend
        .verify_model("synthetic-internal", limits)
        .await
        .unwrap();
    assert_eq!(
        backend
            .tokenize(&request, "synthetic-internal", limits)
            .await
            .unwrap(),
        7
    );
    let bytes = bounded_json(
        backend
            .completion(&request, "synthetic-internal")
            .await
            .unwrap(),
        limits.response_bytes,
    )
    .await
    .unwrap();
    let (value, usage) =
        normalize_completion(&bytes, &request, "synthetic-internal", 7, limits).unwrap();
    assert_eq!(value["model"], "public-alias");
    assert!(value.get("private_diagnostic").is_none());
    assert_eq!(usage.total_tokens, 9);
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 3);
}

#[tokio::test]
async fn actual_http_redirect_errors_oversized_chunked_and_wrong_model_are_safe_without_retry() {
    let fixture = Fixture::new().await;
    let backend = Backend::new(&fixture.configuration).unwrap();
    for (mode, code) in [
        (1, "upstream_unavailable"),
        (2, "upstream_response_invalid"),
        (3, "upstream_response_invalid"),
        (5, "upstream_response_invalid"),
        (6, "upstream_response_invalid"),
        (7, "upstream_unavailable"),
    ] {
        fixture.state.mode.store(mode, Ordering::SeqCst);
        let before = fixture.state.calls.load(Ordering::SeqCst);
        assert_eq!(
            backend
                .verify_model("synthetic-internal", &fixture.configuration.limits)
                .await,
            Err(code)
        );
        assert_eq!(fixture.state.calls.load(Ordering::SeqCst), before + 1);
    }
}

#[tokio::test]
async fn actual_http_stall_times_out_without_replaying_tokenization() {
    let fixture = Fixture::new().await;
    let backend = Backend::new(&fixture.configuration).unwrap();
    fixture.state.mode.store(4, Ordering::SeqCst);
    assert_eq!(
        backend
            .tokenize(
                &fixture.request(),
                "synthetic-internal",
                &fixture.configuration.limits
            )
            .await,
        Err("upstream_timeout")
    );
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 1);
}
