//! Real gateway TLS/HTTP/filesystem with identified synthetic control and model servers, not Go/GPU qualification.
use crate::configuration::{Configuration, Limits};
use crate::outcome::{Outcome, Status};
use crate::routing::Application;
use axum::body::Body;
use axum::extract::{Path as RoutePath, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::{Json, Router};
use futures_util::StreamExt;
use serde_json::{Value, json};
use std::collections::HashSet;
use std::fs;
use std::net::{SocketAddr, SocketAddrV4, TcpListener};
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::sync::{Arc, Mutex};
use std::time::Duration;
use tokio::time::Instant;
use uuid::Uuid;

mod throughput;

const QUOTA_TEST_NOW: i64 = 1_794_116_800;

#[derive(Clone)]
struct Synthetic {
    gateway: Uuid,
    node: Uuid,
    workspace: Uuid,
    client_key: String,
    complete_stream: Arc<tokio::sync::Notify>,
    backend_model: &'static str,
    request_seconds: u64,
    alias: Uuid,
    deployment: Uuid,
    control_mode: Arc<AtomicUsize>,
    backend_mode: Arc<AtomicUsize>,
    model_checks: Arc<AtomicUsize>,
    generations: Arc<AtomicUsize>,
    dropped_generations: Arc<AtomicUsize>,
    admissions: Arc<Mutex<HashSet<Uuid>>>,
    backend_request_ids: Arc<Mutex<Vec<Uuid>>>,
    outcomes: Arc<Mutex<Vec<Outcome>>>,
}
impl Synthetic {
    fn binding(&self) -> Value {
        json!({"id":self.alias,"name":"public-alias","created":1,"deployment_id":self.deployment,"deployment_revision":1,"backend_model":self.backend_model})
    }
}

async fn control(
    State(state): State<Synthetic>,
    RoutePath((gateway, operation)): RoutePath<(Uuid, String)>,
    headers: HeaderMap,
    Json(body): Json<Value>,
) -> Response {
    assert_eq!(gateway, state.gateway);
    assert_eq!(
        headers[header::AUTHORIZATION],
        "Bearer synthetic-machine-secret"
    );
    assert!(body.get("messages").is_none());
    assert!(body.get("prompt").is_none());
    let mode = state.control_mode.load(Ordering::SeqCst);
    if mode == 2 {
        return (
            StatusCode::SERVICE_UNAVAILABLE,
            Json(json!({"code":"SYNTHETIC_OUTAGE"})),
        )
            .into_response();
    }
    if operation == "outcomes" {
        if mode == 6 {
            return (
                StatusCode::SERVICE_UNAVAILABLE,
                Json(json!({"code":"STORAGE_UNAVAILABLE"})),
            )
                .into_response();
        }
        if mode == 4 {
            return (
                StatusCode::CONFLICT,
                Json(json!({"code":"GATEWAY_OUTCOME_CONFLICT"})),
            )
                .into_response();
        }
        if mode == 5 {
            return (
                StatusCode::UNAUTHORIZED,
                Json(json!({"code":"GATEWAY_UNAUTHORIZED"})),
            )
                .into_response();
        }
        let outcome: Outcome = serde_json::from_value(body).unwrap();
        outcome.validate().unwrap();
        assert!(
            state
                .admissions
                .lock()
                .unwrap()
                .contains(&outcome.request_id)
        );
        let digest = if mode == 3 {
            "wrong-receipt".into()
        } else {
            outcome.digest().unwrap()
        };
        let id = outcome.request_id;
        state.outcomes.lock().unwrap().push(outcome);
        return Json(json!({"data":{"request_id":id,"canonical_sha256":digest}})).into_response();
    }
    assert_eq!(body["api_key"], state.client_key);
    if operation == "models" {
        state.model_checks.fetch_add(1, Ordering::SeqCst);
    }
    if mode == 1 {
        return (
            StatusCode::UNAUTHORIZED,
            Json(json!({"code":"API_KEY_INVALID","detail":"private synthetic diagnostic"})),
        )
            .into_response();
    }
    if mode == 7 {
        return (
            StatusCode::TOO_MANY_REQUESTS,
            [(header::RETRY_AFTER, "17")],
            Json(json!({"code":"RATE_LIMITED"})),
        )
            .into_response();
    }
    if operation == "models" {
        return Json(json!({"data":{"gateway_id":gateway,"node_id":state.node,"workspace_id":state.workspace,"models":[state.binding()]}})).into_response();
    }
    assert_eq!(operation, "admissions");
    assert_eq!(body["model"], "public-alias");
    let id = serde_json::from_value(body["request_id"].clone()).unwrap();
    state.admissions.lock().unwrap().insert(id);
    Json(json!({"data":{"request_id":id,"gateway_id":gateway,"node_id":state.node,"workspace_id":state.workspace,"binding":state.binding(),"maximum_context_tokens":2048,"maximum_request_seconds":state.request_seconds}})).into_response()
}

struct GenerationDrop(Arc<AtomicUsize>);
impl Drop for GenerationDrop {
    fn drop(&mut self) {
        self.0.fetch_add(1, Ordering::SeqCst);
    }
}

fn event(choices: Value, usage: Value) -> Vec<u8> {
    format!("data: {}\n\n",json!({"id":"chatcmpl-synthetic","object":"chat.completion.chunk","created":1,"model":"synthetic-internal","choices":choices,"usage":usage,"private_diagnostic":"must not forward"})).into_bytes()
}
async fn backend(State(state): State<Synthetic>, request: axum::extract::Request) -> Response {
    assert!(!request.headers().contains_key(header::AUTHORIZATION));
    if request.uri().path() == "/v1/models" {
        return Json(
            json!({"object":"list","data":[{"id":"synthetic-internal","object":"model"}]}),
        )
        .into_response();
    }
    let tokenize = request.uri().path() == "/tokenize";
    if !tokenize && let Some(value) = request.headers().get("x-request-id") {
        let id = Uuid::parse_str(value.to_str().unwrap()).unwrap();
        assert!(state.admissions.lock().unwrap().contains(&id));
        state.backend_request_ids.lock().unwrap().push(id);
    }
    let body: Value = serde_json::from_slice(
        &axum::body::to_bytes(request.into_body(), 65_536)
            .await
            .unwrap(),
    )
    .unwrap();
    assert_eq!(body["model"], "synthetic-internal");
    assert_eq!(body["chat_template_kwargs"]["enable_thinking"], false);
    if tokenize {
        return Json(json!({"count":7,"tokens":[1,2,3,4,5,6,7],"max_model_len":2048}))
            .into_response();
    }
    state.generations.fetch_add(1, Ordering::SeqCst);
    if body["stream"] == false && state.backend_mode.load(Ordering::SeqCst) == 3 {
        let _owned = GenerationDrop(state.dropped_generations.clone());
        std::future::pending::<()>().await;
    }
    if body["stream"] == false {
        return Json(json!({"id":"chatcmpl-synthetic","object":"chat.completion","created":1,"model":"synthetic-internal","choices":[{"index":0,"message":{"role":"assistant","content":"Synthetic reply \u{4e2d}\u{6587}"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"private_diagnostic":"must not forward"})).into_response();
    }
    assert_eq!(body["stream_options"]["include_usage"], true);
    let mode = state.backend_mode.load(Ordering::SeqCst);
    let stream = async_stream::stream! {
        let _owned=GenerationDrop(state.dropped_generations.clone());
        let first=event(json!([{"index":0,"delta":{"role":"assistant","content":"Synthetic \u{4e2d}\u{6587}"},"finish_reason":null}]),Value::Null);
        yield Ok::<_,std::io::Error>(first);
        if mode==2 {std::future::pending::<()>().await;}
        if mode==4 {state.complete_stream.notified().await;}
        if mode!=1 {
            yield Ok(event(json!([{"index":0,"delta":{},"finish_reason":"stop"}]),Value::Null));
            yield Ok(event(json!([]),json!({"prompt_tokens":7,"completion_tokens":2,"total_tokens":9})));
            yield Ok(crate::streaming::DONE_EVENT.to_vec());
            if mode==5 {yield Ok(b"data: unexpected\n\n".to_vec());}
        }
    };
    (
        [(header::CONTENT_TYPE, "text/event-stream")],
        Body::from_stream(stream),
    )
        .into_response()
}

struct Fixture {
    root: PathBuf,
    app: Option<Arc<Application>>,
    process: Option<std::process::Child>,
    state: Synthetic,
    client: reqwest::Client,
    origin: String,
    gateway_handle: axum_server::Handle<SocketAddr>,
    control_handle: axum_server::Handle<SocketAddr>,
    servers: Vec<tokio::task::JoinHandle<()>>,
}
impl Fixture {
    #[cfg(feature = "process-integration")]
    async fn use_real_process(&mut self) {
        let app = self.app.take().unwrap();
        let configuration = app.configuration.clone();
        app.shutdown();
        self.gateway_handle.shutdown();
        app.finish_workers().await;
        let server = self.servers.pop().unwrap();
        tokio::time::timeout(Duration::from_secs(3), server)
            .await
            .unwrap()
            .unwrap();
        drop(app);
        use std::io::Write;
        use std::os::unix::fs::OpenOptionsExt;
        let mut file = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(self.root.join("gateway.json"))
            .unwrap();
        let mut base = configuration.clone();
        base.backend_address = "127.0.0.1:1".parse().unwrap();
        base.limits.request_seconds = 90;
        file.write_all(&serde_json::to_vec(&base).unwrap()).unwrap();
        file.sync_all().unwrap();
        let mut environment = fs::OpenOptions::new()
            .write(true)
            .create_new(true)
            .mode(0o600)
            .open(self.root.join("gateway.env"))
            .unwrap();
        write!(environment,"PROJECT_ONE_GATEWAY_BACKEND_ADDRESS={}\nPROJECT_ONE_GATEWAY_LIMIT_REQUEST_SECONDS={}\n",configuration.backend_address,configuration.limits.request_seconds).unwrap();
        environment.sync_all().unwrap();
        self.start_process().await;
    }
    #[cfg(feature = "process-integration")]
    async fn start_process(&mut self) {
        use std::os::unix::fs::OpenOptionsExt;
        let binary = PathBuf::from(
            std::env::var_os("PROJECT_ONE_GATEWAY_BINARY")
                .expect("explicit built gateway binary required for process profile"),
        );
        assert_eq!(binary.canonicalize().unwrap(), binary);
        let version = std::process::Command::new(&binary)
            .arg("--version")
            .output()
            .unwrap();
        assert!(version.status.success());
        assert_eq!(
            String::from_utf8(version.stdout).unwrap(),
            format!("projectone-gateway {}\n", env!("CARGO_PKG_VERSION"))
        );
        let log = fs::OpenOptions::new()
            .create(true)
            .append(true)
            .mode(0o600)
            .open(self.root.join("process.log"))
            .unwrap();
        let child = std::process::Command::new(binary)
            .arg("serve")
            .arg("--config")
            .arg(self.root.join("gateway.json"))
            .arg("--env-file")
            .arg(self.root.join("gateway.env"))
            .stdin(std::process::Stdio::null())
            .stdout(log.try_clone().unwrap())
            .stderr(log)
            .spawn()
            .unwrap();
        self.process = Some(child);
        let deadline = Instant::now() + Duration::from_secs(3);
        loop {
            assert!(
                self.process.as_mut().unwrap().try_wait().unwrap().is_none(),
                "owned gateway process exited before readiness"
            );
            if self
                .client
                .get(format!("{}/health/live", self.origin))
                .send()
                .await
                .is_ok()
            {
                break;
            }
            assert!(Instant::now() < deadline);
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    }
    async fn new() -> Self {
        Self::with_quota(None).await
    }
    async fn with_quota(quota_limits: Option<Value>) -> Self {
        Self::start(quota_limits, None).await
    }

    async fn start(quota_limits: Option<Value>, vllm: Option<SocketAddrV4>) -> Self {
        crate::control::initialize_tls_provider();
        let root = std::env::temp_dir().join(format!("gateway-routing-{}", Uuid::new_v4()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let root = root.canonicalize().unwrap();
        let state_directory = root.join("state");
        fs::create_dir(&state_directory).unwrap();
        fs::set_permissions(&state_directory, fs::Permissions::from_mode(0o700)).unwrap();
        let generated = rcgen::generate_simple_self_signed(vec!["127.0.0.1".into()]).unwrap();
        let certificate = generated.cert.pem().into_bytes();
        let key = generated.signing_key.serialize_pem().into_bytes();
        for (name, bytes) in [
            ("ca.pem", certificate.clone()),
            ("certificate.pem", certificate.clone()),
            ("key.pem", key.clone()),
            ("credential", b"synthetic-machine-secret\n".to_vec()),
        ] {
            fs::write(root.join(name), bytes).unwrap();
            fs::set_permissions(root.join(name), fs::Permissions::from_mode(0o600)).unwrap();
        }
        let key_id = Uuid::new_v4();
        let client_key = if quota_limits.is_some() {
            format!("pk1.{key_id}.{}", Uuid::new_v4())
        } else {
            "synthetic-client-secret".to_owned()
        };
        let (backend_model, request_seconds) = if vllm.is_some() {
            ("projectone-qwen3-0.6b", 60)
        } else {
            ("synthetic-internal", 5)
        };
        let state = Synthetic {
            gateway: Uuid::new_v4(),
            node: Uuid::new_v4(),
            workspace: Uuid::new_v4(),
            client_key,
            complete_stream: Arc::default(),
            backend_model,
            request_seconds,
            alias: Uuid::new_v4(),
            deployment: Uuid::new_v4(),
            control_mode: Arc::default(),
            backend_mode: Arc::default(),
            model_checks: Arc::default(),
            generations: Arc::default(),
            dropped_generations: Arc::default(),
            admissions: Arc::default(),
            backend_request_ids: Arc::default(),
            outcomes: Arc::default(),
        };
        let control_listener = TcpListener::bind("127.0.0.1:0").unwrap();
        control_listener.set_nonblocking(true).unwrap();
        let control_address = control_listener.local_addr().unwrap();
        let tls = axum_server::tls_rustls::RustlsConfig::from_pem(certificate.clone(), key)
            .await
            .unwrap();
        let control_handle = axum_server::Handle::new();
        let control_server_handle = control_handle.clone();
        let control_router = Router::new()
            .route(
                "/gateway/v1/sites/default/cells/default/instances/{gateway}/{operation}",
                axum::routing::post(control),
            )
            .with_state(state.clone());
        let control_server = tokio::spawn(async move {
            axum_server::from_tcp_rustls(control_listener, tls)
                .unwrap()
                .handle(control_server_handle)
                .serve(control_router.into_make_service())
                .await
                .unwrap();
        });
        let (backend_address, backend_server) = if let Some(address) = vllm {
            (address, None)
        } else {
            let listener = tokio::net::TcpListener::bind("127.0.0.1:0").await.unwrap();
            let SocketAddr::V4(address) = listener.local_addr().unwrap() else {
                panic!("IPv4 fixture")
            };
            let router = Router::new().fallback(backend).with_state(state.clone());
            let server = tokio::spawn(async move {
                axum::serve(listener, router).await.unwrap();
            });
            (address, Some(server))
        };
        let gateway_listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let SocketAddr::V4(listen_address) = gateway_listener.local_addr().unwrap() else {
            panic!("IPv4 fixture")
        };
        let scoped = quota_limits.is_some();
        let configuration = Configuration {
            shared_quota: None,
            quota_policy: quota_limits.map(|limits| {
                serde_json::from_value(json!({
                    "tenants": [{"id": Uuid::new_v4(), "workspaces": [state.workspace],
                        "applications": [{"id": Uuid::new_v4(), "limits": limits,
                            "keys": [{"id": key_id, "workspace_id": state.workspace}]}]}]
                }))
                .unwrap()
            }),
            gateway_id: state.gateway,
            node_id: state.node,
            listen_address,
            backend_address,
            control_origin: format!("https://{control_address}"),
            site_id: "default".to_owned(),
            cell_id: "default".to_owned(),
            control_ca_file: root.join("ca.pem"),
            gateway_credential_file: root.join("credential"),
            tls_certificate_file: root.join("certificate.pem"),
            tls_private_key_file: root.join("key.pem"),
            state_directory,
            limits: Limits {
                active_requests: if scoped { 2 } else { 1 },
                backend_idle_connections: 1,
                queued_requests: 1,
                queue_milliseconds: 500,
                connect_seconds: 1,
                control_seconds: 1,
                request_seconds,
                idle_seconds: if vllm.is_some() {
                    15
                } else if scoped {
                    3
                } else {
                    1
                },
                header_seconds: 1,
                ..Limits::default()
            },
        };
        let app = Application::with_quota_clock(configuration.clone(), || QUOTA_TEST_NOW).unwrap();
        let gateway_handle = axum_server::Handle::new();
        let server_app = app.clone();
        let server_handle = gateway_handle.clone();
        let gateway_server = tokio::spawn(async move {
            crate::listener::serve(&configuration, server_app, gateway_listener, server_handle)
                .await
                .unwrap();
        });
        let client = reqwest::Client::builder()
            .tls_certs_only(reqwest::Certificate::from_pem_bundle(&certificate).unwrap())
            .http1_only()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .timeout(Duration::from_secs(request_seconds + 3))
            .build()
            .unwrap();
        let origin = format!("https://{listen_address}");
        let fixture = Self {
            root,
            app: Some(app),
            process: None,
            state,
            client,
            origin,
            gateway_handle,
            control_handle,
            servers: std::iter::once(control_server)
                .chain(backend_server)
                .chain(std::iter::once(gateway_server))
                .collect(),
        };
        tokio::time::timeout(Duration::from_secs(3), async {
            loop {
                if fixture
                    .client
                    .get(format!("{}/health/live", fixture.origin))
                    .send()
                    .await
                    .is_ok()
                {
                    break;
                }
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        fixture
    }
    fn app(&self) -> &Arc<Application> {
        self.app.as_ref().expect("in-process fixture")
    }
    fn chat(&self, stream: bool) -> reqwest::RequestBuilder {
        self.chat_at(&self.origin, stream)
    }

    fn chat_at(&self, origin: &str, stream: bool) -> reqwest::RequestBuilder {
        self.client.post(format!("{origin}/v1/chat/completions")).bearer_auth(&self.state.client_key)
            .json(&json!({"model":"public-alias","messages":[{"role":"user","content":"Synthetic request \u{4e2d}\u{6587}"}],"max_tokens":16,"stream":stream}))
    }
}
impl Drop for Fixture {
    fn drop(&mut self) {
        if let Some(app) = &self.app {
            app.shutdown();
        }
        if let Some(child) = &mut self.process {
            let _ = child.kill();
            let _ = child.wait();
        }
        self.gateway_handle.shutdown();
        self.control_handle.shutdown();
        for server in &self.servers {
            server.abort();
        }
        fs::remove_dir_all(&self.root).unwrap();
    }
}
async fn wait_until(mut condition: impl FnMut() -> bool) {
    let deadline = Instant::now() + Duration::from_secs(3);
    while !condition() {
        assert!(
            Instant::now() < deadline,
            "bounded observable condition did not arrive"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
}

#[tokio::test]
async fn real_gateway_tls_models_chat_sse_and_durable_metadata_receipts() {
    let fixture = Fixture::new().await;
    let models: Value = fixture
        .client
        .get(format!("{}/v1/models", fixture.origin))
        .bearer_auth("synthetic-client-secret")
        .send()
        .await
        .unwrap()
        .json()
        .await
        .unwrap();
    assert_eq!(models["data"][0]["id"], "public-alias");
    assert!(!models.to_string().contains("synthetic-internal"));
    let caller_id = Uuid::new_v4();
    let response = fixture
        .chat(false)
        .header("x-request-id", caller_id.to_string())
        .send()
        .await
        .unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    assert_eq!(response.headers()[header::CACHE_CONTROL], "no-store");
    let id = Uuid::parse_str(response.headers()["x-request-id"].to_str().unwrap()).unwrap();
    assert_ne!(id, caller_id);
    assert_eq!(*fixture.state.backend_request_ids.lock().unwrap(), [id]);
    let value: Value = response.json().await.unwrap();
    assert_eq!(value["model"], "public-alias");
    assert_eq!(value["usage"]["total_tokens"], 9);
    assert!(value.get("private_diagnostic").is_none());
    let text = fixture
        .chat(true)
        .send()
        .await
        .unwrap()
        .text()
        .await
        .unwrap();
    assert_eq!(text.matches("data: [DONE]").count(), 1);
    assert!(text.ends_with("data: [DONE]\n\n"));
    assert!(text.contains("Synthetic \u{4e2d}\u{6587}"));
    assert!(!text.contains("private_diagnostic"));
    assert!(!text.contains("synthetic-internal"));
    assert_eq!(fixture.state.admissions.lock().unwrap().len(), 2);
    assert_eq!(crate::reporting::flush(fixture.app()).await.unwrap(), 2);
    assert!(fixture.app().spool.next_pending().unwrap().is_none());
    let outcomes = fixture.state.outcomes.lock().unwrap();
    assert!(
        outcomes
            .iter()
            .all(|value| value.status == Status::Succeeded
                && value.usage.as_ref().unwrap().total_tokens == 9)
    );
}

#[tokio::test]
async fn real_gateway_tls_negotiates_http1_with_a_client_that_prefers_http2() {
    // curl, Go's net/http and browsers offer h2 first; a listener that accepted it would drop
    // the connection after the handshake, because the server speaks HTTP/1.1 only.
    let fixture = Fixture::new().await;
    let address = fixture.origin.trim_start_matches("https://").to_owned();
    let authority = fs::read(fixture.root.join("ca.pem")).unwrap();
    let (negotiated, head) = tokio::task::spawn_blocking(move || {
        use rustls::pki_types::{CertificateDer, ServerName, pem::PemObject};
        use std::io::{Read, Write};
        let mut roots = rustls::RootCertStore::empty();
        for certificate in CertificateDer::pem_slice_iter(&authority) {
            roots.add(certificate.unwrap()).unwrap();
        }
        let mut client = rustls::ClientConfig::builder()
            .with_root_certificates(roots)
            .with_no_client_auth();
        client.alpn_protocols = vec![b"h2".to_vec(), b"http/1.1".to_vec()];
        let name = ServerName::try_from("127.0.0.1").unwrap();
        let connection = rustls::ClientConnection::new(Arc::new(client), name).unwrap();
        let socket = std::net::TcpStream::connect(&address).unwrap();
        socket
            .set_read_timeout(Some(Duration::from_secs(3)))
            .unwrap();
        let mut stream = rustls::StreamOwned::new(connection, socket);
        stream
            .write_all(b"GET /health/live HTTP/1.1\r\nHost: 127.0.0.1\r\nConnection: close\r\n\r\n")
            .unwrap();
        let mut response = Vec::new();
        let _ = stream.read_to_end(&mut response);
        let negotiated = stream.conn.alpn_protocol().map(<[u8]>::to_vec);
        (negotiated, String::from_utf8_lossy(&response).into_owned())
    })
    .await
    .unwrap();
    assert_eq!(negotiated.as_deref(), Some(&b"http/1.1"[..]));
    assert!(head.starts_with("HTTP/1.1 200"), "{head}");
}

#[tokio::test]
async fn final_usage_requires_a_valid_stream_and_durable_outcome() {
    let fixture = Fixture::new().await;
    let request = || {
        fixture.chat(true).json(&json!({
            "model": "public-alias", "stream": true, "max_tokens": 16,
            "messages": [{"role": "user", "content": "Synthetic prompt"}],
            "stream_options": {"include_usage": true}
        }))
    };
    fixture.state.backend_mode.store(5, Ordering::SeqCst);
    let body = request().send().await.unwrap().text().await.unwrap();
    assert!(body.contains("Synthetic \u{4e2d}\u{6587}"));
    assert!(body.contains("upstream_response_invalid"));
    assert!(!body.contains("total_tokens"));
    assert!(!body.contains("[DONE]"));
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::InvalidResponse);
    assert!(outcome.usage.is_none());
    assert_eq!(crate::reporting::flush(fixture.app()).await.unwrap(), 1);

    fixture.state.backend_mode.store(4, Ordering::SeqCst);
    let mut response = request().send().await.unwrap();
    let id = response.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    // Wait until generation has started, then force the atomic spool publication to fail.
    assert!(response.chunk().await.unwrap().is_some());
    let stage = fixture
        .app()
        .configuration
        .state_directory
        .join("outcomes")
        .join(format!(".stage-{id}.json"));
    fs::write(&stage, b"occupied").unwrap();
    fixture.state.complete_stream.notify_one();
    let tail = response.text().await.unwrap();
    assert!(tail.contains("usage_storage_unavailable"));
    assert!(!tail.contains("total_tokens"));
    assert!(!tail.contains("[DONE]"));
    assert!(!fixture.app().spool.ready());
    assert_eq!(fs::read(stage).unwrap(), b"occupied");
}

#[tokio::test]
async fn real_gateway_incomplete_sse_and_actual_client_disconnect_never_fabricate_success() {
    let fixture = Fixture::new().await;
    fixture.state.backend_mode.store(1, Ordering::SeqCst);
    let text = fixture
        .chat(true)
        .send()
        .await
        .unwrap()
        .text()
        .await
        .unwrap();
    assert!(text.contains("upstream_response_invalid"));
    assert!(!text.contains("[DONE]"));
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::InvalidResponse);
    assert!(outcome.usage.is_none());
    crate::reporting::flush(fixture.app()).await.unwrap();
    fixture.state.backend_mode.store(2, Ordering::SeqCst);
    let drops = fixture.state.dropped_generations.load(Ordering::SeqCst);
    let mut stream = fixture.chat(true).send().await.unwrap().bytes_stream();
    assert!(stream.next().await.unwrap().is_ok());
    drop(stream);
    wait_until(|| {
        fixture.state.dropped_generations.load(Ordering::SeqCst) > drops
            && fixture.app().spool.next_pending().unwrap().is_some()
    })
    .await;
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::ClientCancelled);
    assert!(outcome.usage.is_none());
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn real_gateway_reauthorizes_after_queue_and_control_outage_blocks_new_backend_effects() {
    let fixture = Fixture::new().await;
    fixture.state.backend_mode.store(2, Ordering::SeqCst);
    let mut stream = fixture.chat(true).send().await.unwrap().bytes_stream();
    stream.next().await.unwrap().unwrap();
    let queued = fixture.chat(false);
    let task = tokio::spawn(async move { queued.send().await.unwrap() });
    wait_until(|| fixture.state.model_checks.load(Ordering::SeqCst) >= 2).await;
    fixture.state.control_mode.store(1, Ordering::SeqCst);
    drop(stream);
    let response = task.await.unwrap();
    assert_eq!(response.status(), StatusCode::UNAUTHORIZED);
    assert!(response.text().await.unwrap().contains("invalid_api_key"));
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    assert_eq!(fixture.state.admissions.lock().unwrap().len(), 1);
    fixture.state.control_mode.store(2, Ordering::SeqCst);
    let response = fixture.chat(false).send().await.unwrap();
    assert_eq!(response.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    fixture.app().shutdown();
    fixture.app().finish_workers().await;
}

#[tokio::test]
async fn real_gateway_nonstream_disconnect_drops_actual_upstream_before_generation_deadline() {
    let fixture = Fixture::new().await;
    fixture.state.backend_mode.store(3, Ordering::SeqCst);
    let request = fixture.chat(false);
    let task = tokio::spawn(async move { request.send().await });
    wait_until(|| fixture.state.generations.load(Ordering::SeqCst) == 1).await;
    task.abort();
    let _ = task.await;
    wait_until(|| {
        fixture.state.dropped_generations.load(Ordering::SeqCst) == 1
            && fixture.app().spool.next_pending().unwrap().is_some()
    })
    .await;
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::ClientCancelled);
    assert!(outcome.usage.is_none());
}

#[tokio::test]
async fn real_gateway_shutdown_drops_stream_and_persists_shutdown_metadata() {
    let fixture = Fixture::new().await;
    fixture.state.backend_mode.store(2, Ordering::SeqCst);
    let mut stream = fixture.chat(true).send().await.unwrap().bytes_stream();
    stream.next().await.unwrap().unwrap();
    fixture.app().shutdown();
    fixture.app().finish_workers().await;
    wait_until(|| fixture.state.dropped_generations.load(Ordering::SeqCst) == 1).await;
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::GatewayShutdown);
    assert!(outcome.usage.is_none());
    let mut remaining = Vec::new();
    while let Some(chunk) = stream.next().await {
        remaining.extend_from_slice(&chunk.unwrap());
    }
    assert!(!String::from_utf8(remaining).unwrap().contains("[DONE]"));
    assert_eq!(
        fixture.chat(false).send().await.unwrap().status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
}

#[tokio::test]
async fn real_gateway_outage_retains_metadata_and_wrong_receipt_degrades_readiness() {
    let fixture = Fixture::new().await;
    assert_eq!(
        fixture.chat(false).send().await.unwrap().status(),
        StatusCode::OK
    );
    let digest = fixture
        .app()
        .spool
        .next_pending()
        .unwrap()
        .unwrap()
        .digest()
        .unwrap();
    fixture.state.control_mode.store(2, Ordering::SeqCst);
    assert!(crate::reporting::flush(fixture.app()).await.is_err());
    assert_eq!(
        fixture
            .app()
            .spool
            .next_pending()
            .unwrap()
            .unwrap()
            .digest()
            .unwrap(),
        digest
    );
    fixture.state.control_mode.store(3, Ordering::SeqCst);
    assert_eq!(
        crate::reporting::flush(fixture.app()).await,
        Err("GATEWAY_OUTCOME_ACKNOWLEDGMENT_INVALID")
    );
    assert!(!fixture.app().spool.ready());
    assert_eq!(
        fixture
            .client
            .get(format!("{}/health/ready", fixture.origin))
            .send()
            .await
            .unwrap()
            .status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(
        fixture
            .app()
            .spool
            .next_pending()
            .unwrap()
            .unwrap()
            .digest()
            .unwrap(),
        digest
    );
    fixture.state.control_mode.store(0, Ordering::SeqCst);
    assert_eq!(crate::reporting::flush(fixture.app()).await.unwrap(), 1);
    assert!(fixture.app().spool.next_pending().unwrap().is_none());
    assert!(!fixture.app().spool.ready()); // Restart after correcting a receipt-integrity fault requires operator review.
}

#[tokio::test]
async fn actual_tls_permanent_outcome_rejection_preserves_metadata_and_refuses_new_admission() {
    for (mode, code) in [
        (4, "GATEWAY_OUTCOME_REJECTED"),
        (5, "GATEWAY_OUTCOME_AUTHORITY_INVALIDATED"),
    ] {
        let fixture = Fixture::new().await;
        assert_eq!(
            fixture.chat(false).send().await.unwrap().status(),
            StatusCode::OK
        );
        let digest = fixture
            .app()
            .spool
            .next_pending()
            .unwrap()
            .unwrap()
            .digest()
            .unwrap();
        fixture.state.control_mode.store(mode, Ordering::SeqCst);
        assert_eq!(crate::reporting::flush(fixture.app()).await, Err(code));
        assert!(!fixture.app().spool.ready());
        assert_eq!(
            fixture
                .app()
                .spool
                .next_pending()
                .unwrap()
                .unwrap()
                .digest()
                .unwrap(),
            digest
        );
        assert_eq!(
            fixture.chat(false).send().await.unwrap().status(),
            StatusCode::SERVICE_UNAVAILABLE
        );
        assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    }
}

#[tokio::test]
async fn real_upstream_with_unread_bounded_publisher_times_out_and_releases_permits() {
    let fixture = Fixture::new().await;
    let request=crate::protocol::ChatRequest::parse(&serde_json::to_vec(&json!({"model":"public-alias","messages":[{"role":"user","content":"Synthetic request \u{4e2d}\u{6587}"}],"max_tokens":16,"stream":true})).unwrap(),&fixture.app().configuration.limits).unwrap();
    let id = Uuid::new_v4();
    fixture.state.admissions.lock().unwrap().insert(id);
    let response = fixture
        .app()
        .backend
        .completion(&request, "synthetic-internal", id)
        .await
        .unwrap();
    let execution = crate::admission::Execution::new(fixture.app().spool.reserve(id).unwrap(), id);
    let semaphore = Arc::new(tokio::sync::Semaphore::new(2));
    let (sender, _unread_receiver) = tokio::sync::mpsc::channel(1);
    let work = crate::transport::StreamWork {
        response,
        decoder: crate::streaming::Decoder::new(
            &request,
            "synthetic-internal",
            7,
            &fixture.app().configuration.limits,
        ),
        execution,
        sender,
        shutdown: fixture.app().receiver(),
        deadline: Instant::now() + Duration::from_secs(5),
        idle: Duration::from_secs(1),
        request_id: id,
        ingress: semaphore.clone().acquire_owned().await.unwrap(),
        active: semaphore.clone().acquire_owned().await.unwrap(),
    };
    tokio::time::timeout(Duration::from_secs(4), work.run())
        .await
        .unwrap();
    assert_eq!(semaphore.available_permits(), 2);
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::ClientCancelled);
    assert!(outcome.usage.is_none());
}

#[tokio::test]
#[ignore = "requires an offline vLLM engine with the approved model and served name"]
async fn offline_vllm_stream_reports_real_tokens_and_durable_usage() {
    let backend = std::env::var("OWNSOLID_TEST_VLLM_ADDRESS")
        .expect("set OWNSOLID_TEST_VLLM_ADDRESS to the offline loopback backend")
        .parse::<SocketAddrV4>()
        .unwrap();
    assert!(backend.ip().is_loopback() && backend.port() != 0);
    let fixture = Fixture::start(None, Some(backend)).await;
    let response = fixture.chat(true).json(&json!({
        "model": "public-alias", "stream": true,
        "messages": [{"role": "user", "content": "What color is the daytime sky? Give one word."}],
        "temperature": 0, "max_tokens": 16, "stream_options": {"include_usage": true}
    })).send().await.unwrap();
    let status = response.status();
    let request_id = response.headers()["x-request-id"]
        .to_str()
        .unwrap()
        .to_owned();
    let completion_id = format!("chatcmpl-{request_id}");
    let body = response.text().await.unwrap();
    assert_eq!(status, StatusCode::OK, "{body}");
    let events = body.strip_suffix("data: [DONE]\n\n").expect(&body);
    let mut output_bytes = 0;
    let mut usage = None;
    for event in events.split("\n\n").filter(|event| !event.is_empty()) {
        let chunk: Value = serde_json::from_str(event.strip_prefix("data: ").unwrap()).unwrap();
        assert_eq!(chunk["model"], "public-alias");
        assert_eq!(chunk["id"], completion_id);
        if let Some(text) = chunk
            .pointer("/choices/0/delta/content")
            .and_then(Value::as_str)
        {
            output_bytes += text.len();
        }
        if !chunk["usage"].is_null() {
            assert!(usage.is_none());
            usage = Some(
                serde_json::from_value::<crate::protocol::Usage>(chunk["usage"].clone()).unwrap(),
            );
        }
    }
    assert!(output_bytes > 0);
    let usage = usage.expect("validated final usage chunk");
    assert!(usage.prompt_tokens > 0);
    assert!((1..=16).contains(&usage.completion_tokens));
    assert_eq!(
        usage.total_tokens,
        usage.prompt_tokens + usage.completion_tokens
    );
    let outcome = fixture.app().spool.next_pending().unwrap().unwrap();
    assert_eq!(outcome.status, Status::Succeeded);
    assert_eq!(outcome.usage, Some(usage.clone()));
    assert_eq!(outcome.request_id.to_string(), request_id);
    println!(
        "offline vLLM: {} prompt tokens, {} completion tokens, correlated durable outcome",
        usage.prompt_tokens, usage.completion_tokens
    );
}

#[tokio::test]
async fn stream_reserves_before_generation_and_refunds_verified_usage() {
    let fixture = Fixture::with_quota(Some(json!({"tokens": {"daily": 32}}))).await;
    fixture.state.backend_mode.store(4, Ordering::SeqCst);
    let pending = fixture.chat(true).send().await.unwrap();
    assert_eq!(pending.status(), StatusCode::OK);

    // The first call holds 23 tokens while the upstream stream is incomplete.
    let denied = fixture.chat(false).send().await.unwrap();
    assert_eq!(denied.status(), StatusCode::TOO_MANY_REQUESTS);
    let retry = 86_400 - QUOTA_TEST_NOW.rem_euclid(86_400);
    assert_eq!(denied.headers()[header::RETRY_AFTER], retry.to_string());
    let error: Value = denied.json().await.unwrap();
    assert_eq!(error["error"]["code"], "daily_token_budget_exceeded");
    assert!(
        error["error"]["message"]
            .as_str()
            .unwrap()
            .contains("application daily token budget")
    );
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);

    fixture.state.complete_stream.notify_one();
    let completed = pending.text().await.unwrap();
    assert!(completed.ends_with("data: [DONE]\n\n"));
    assert!(!completed.contains(r#""total_tokens""#));
    // The successful stream charged only 9 actual tokens, leaving 23 available.
    assert_eq!(
        fixture.chat(false).send().await.unwrap().status(),
        StatusCode::OK
    );
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 2);
}

#[tokio::test]
async fn incomplete_stream_keeps_debit_and_releases_concurrency() {
    let fixture = Fixture::with_quota(Some(json!({
        "concurrent_requests": 1, "tokens": {"monthly": 32}
    })))
    .await;
    fixture.state.backend_mode.store(4, Ordering::SeqCst);
    let pending = fixture.chat(true).send().await.unwrap();
    let denied = fixture.chat(false).send().await.unwrap();
    assert_eq!(denied.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(denied.headers()[header::RETRY_AFTER], "1");
    let error: Value = denied.json().await.unwrap();
    assert_eq!(error["error"]["code"], "concurrency_limit_exceeded");

    drop(pending);
    // Wait for the execution guard to drop, without spending any tokens on these checks.
    tokio::time::timeout(Duration::from_secs(3), async {
        loop {
            let denied = fixture.chat(false).send().await.unwrap();
            assert_eq!(denied.status(), StatusCode::TOO_MANY_REQUESTS);
            let body: Value = denied.json().await.unwrap();
            if body["error"]["code"] == "monthly_token_budget_exceeded" {
                break;
            }
            assert_eq!(body["error"]["code"], "concurrency_limit_exceeded");
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await
    .unwrap();
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
}

#[tokio::test]
async fn control_throttle_preserves_retry_after() {
    let fixture = Fixture::new().await;
    fixture.state.control_mode.store(7, Ordering::SeqCst);
    let denied = fixture.chat(false).send().await.unwrap();
    assert_eq!(denied.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(denied.headers()[header::RETRY_AFTER], "17");
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 0);
}

#[cfg(feature = "process-integration")]
#[tokio::test]
async fn actual_binary_sigkill_restart_redelivers_metadata_without_replaying_and_sigterm_drains() {
    let mut fixture = Fixture::new().await;
    fixture.use_real_process().await;
    let response = fixture.chat(false).send().await.unwrap();
    assert_eq!(response.status(), StatusCode::OK);
    let value: Value = response.json().await.unwrap();
    assert_eq!(value["usage"]["total_tokens"], 9);
    fixture.state.control_mode.store(2, Ordering::SeqCst);
    let directory = fixture.root.join("state/outcomes");
    let files = fs::read_dir(&directory)
        .unwrap()
        .map(|entry| entry.unwrap().path())
        .collect::<Vec<_>>();
    assert_eq!(files.len(), 1);
    let original = fs::read(&files[0]).unwrap();
    let record: Value = serde_json::from_slice(&original).unwrap();
    let id: Uuid = serde_json::from_value(record["outcome"]["request_id"].clone()).unwrap();
    assert!(
        !String::from_utf8(original.clone())
            .unwrap()
            .contains("Synthetic request")
    );
    assert!(
        !String::from_utf8(original.clone())
            .unwrap()
            .contains("synthetic-client-secret")
    );
    let mut killed = fixture.process.take().unwrap();
    killed.kill().unwrap();
    assert!(!killed.wait().unwrap().success());
    assert_eq!(fs::read(&files[0]).unwrap(), original);
    fixture.start_process().await;
    assert_eq!(
        fixture.chat(false).send().await.unwrap().status(),
        StatusCode::SERVICE_UNAVAILABLE
    );
    assert_eq!(fs::read(&files[0]).unwrap(), original);
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    fixture.state.control_mode.store(0, Ordering::SeqCst);
    wait_until(|| {
        fixture
            .state
            .outcomes
            .lock()
            .unwrap()
            .iter()
            .any(|value| value.request_id == id)
            && !files[0].exists()
    })
    .await;
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    fixture.state.backend_mode.store(2, Ordering::SeqCst);
    let readiness_deadline = Instant::now() + Duration::from_secs(3);
    loop {
        if fixture
            .client
            .get(format!("{}/health/ready", fixture.origin))
            .send()
            .await
            .unwrap()
            .status()
            == StatusCode::OK
        {
            break;
        }
        assert!(Instant::now() < readiness_deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    let mut stream = fixture.chat(true).send().await.unwrap().bytes_stream();
    stream.next().await.unwrap().unwrap();
    let pid = fixture.process.as_ref().unwrap().id();
    assert!(
        std::process::Command::new("kill")
            .args(["-TERM", &pid.to_string()])
            .status()
            .unwrap()
            .success()
    );
    let deadline = Instant::now() + Duration::from_secs(8);
    loop {
        if let Some(status) = fixture.process.as_mut().unwrap().try_wait().unwrap() {
            assert!(status.success());
            break;
        }
        assert!(
            Instant::now() < deadline,
            "owned gateway did not exit after SIGTERM"
        );
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    fixture.process.take();
    assert!(
        fixture
            .state
            .outcomes
            .lock()
            .unwrap()
            .iter()
            .any(|value| value.status == Status::GatewayShutdown && value.usage.is_none())
    );
    assert_eq!(fs::read_dir(directory).unwrap().count(), 0);
    let log = fs::read_to_string(fixture.root.join("process.log")).unwrap();
    assert!(!log.contains("synthetic-client-secret"));
    assert!(!log.contains("Synthetic request"));
    let evidence = std::env::var_os("PROJECT_ONE_GATEWAY_PROCESS_EVIDENCE")
        .expect("explicit process evidence location required");
    fs::write(evidence,serde_json::to_vec_pretty(&json!({"status":"passed_real_native_gateway_process","scope":"Actual macOS gateway binary/private TLS/filesystem; synthetic control/model replies. No Go/GPU qualification.","assertions":["durable outcome precedes client success","SIGKILL retains exact metadata","restart re-delivers receipt without inference replay","control outage blocks new inference","SIGTERM cancels stream, delivers unknown usage and exits successfully","no prompt/key in metadata or process log"],"generations":fixture.state.generations.load(Ordering::SeqCst),"model_generation_executed":false})).unwrap()).unwrap();
}

#[cfg(feature = "process-integration")]
#[tokio::test]
async fn actual_binary_fence_refuses_suspended_owner_blocks_restart_and_delivers_only_metadata() {
    async fn command(arguments: Vec<std::ffi::OsString>) -> std::process::Output {
        let binary = std::env::var_os("PROJECT_ONE_GATEWAY_BINARY")
            .expect("explicit built gateway binary required");
        tokio::task::spawn_blocking(move || {
            std::process::Command::new(binary)
                .args(arguments)
                .output()
                .unwrap()
        })
        .await
        .unwrap()
    }
    let mut fixture = Fixture::new().await;
    fixture.use_real_process().await;
    let config = fixture.root.join("gateway.json");
    let receipt_path = fixture.root.join("operator-fence-receipt.json");
    let challenge = Uuid::new_v4();
    let arguments = vec![
        "fence".into(),
        "--config".into(),
        config.clone().into_os_string(),
        "--challenge".into(),
        challenge.to_string().into(),
        "--receipt-file".into(),
        receipt_path.clone().into_os_string(),
    ];
    let pid = fixture.process.as_ref().unwrap().id();
    assert!(
        std::process::Command::new("kill")
            .args(["-STOP", &pid.to_string()])
            .status()
            .unwrap()
            .success()
    );
    let refused = command(arguments.clone()).await;
    assert!(!refused.status.success());
    assert!(
        String::from_utf8(refused.stderr)
            .unwrap()
            .contains("GATEWAY_STATE_LOCKED")
    );
    assert!(!receipt_path.exists());
    assert!(
        std::process::Command::new("kill")
            .args(["-CONT", &pid.to_string()])
            .status()
            .unwrap()
            .success()
    );
    fixture.state.control_mode.store(6, Ordering::SeqCst);
    assert_eq!(
        fixture.chat(false).send().await.unwrap().status(),
        StatusCode::OK
    );
    assert!(
        std::process::Command::new("kill")
            .args(["-TERM", &pid.to_string()])
            .status()
            .unwrap()
            .success()
    );
    let deadline = Instant::now() + Duration::from_secs(8);
    loop {
        if let Some(status) = fixture.process.as_mut().unwrap().try_wait().unwrap() {
            assert!(status.success());
            break;
        }
        assert!(Instant::now() < deadline);
        tokio::time::sleep(Duration::from_millis(10)).await;
    }
    fixture.process.take();
    let result = command(arguments.clone()).await;
    assert!(
        result.status.success(),
        "{}",
        String::from_utf8_lossy(&result.stderr)
    );
    let receipt: crate::storage::FenceReceipt =
        serde_json::from_slice(&fs::read(&receipt_path).unwrap()).unwrap();
    assert_eq!(receipt.challenge_id, challenge);
    assert_eq!(receipt.gateway_id, fixture.state.gateway);
    assert!(receipt.persistent_fence_confirmed && receipt.exclusive_state_lock_confirmed);
    assert_eq!(receipt.pending_outcomes, 1);
    let marker = fs::read(fixture.root.join("state/fence.json")).unwrap();
    let repeated = command(arguments).await;
    assert!(!repeated.status.success());
    assert!(
        String::from_utf8(repeated.stderr)
            .unwrap()
            .contains("GATEWAY_RECEIPT_ALREADY_EXISTS")
    );
    let inspected = command(vec![
        "inspect".into(),
        "--config".into(),
        config.clone().into_os_string(),
    ])
    .await;
    assert!(inspected.status.success());
    let value: Value = serde_json::from_slice(&inspected.stdout).unwrap();
    assert_eq!(value["fenced"], true);
    assert_eq!(value["pending_outcomes"], 1);
    assert_eq!(value["delivery_only"], true);
    let blocked = command(vec![
        "serve".into(),
        "--config".into(),
        config.clone().into_os_string(),
    ])
    .await;
    assert!(!blocked.status.success());
    assert!(
        String::from_utf8(blocked.stderr)
            .unwrap()
            .contains("GATEWAY_STATE_FENCED")
    );
    let failed = command(vec![
        "deliver-outcomes".into(),
        "--config".into(),
        config.clone().into_os_string(),
    ])
    .await;
    assert!(!failed.status.success());
    assert_eq!(
        fs::read_dir(fixture.root.join("state/outcomes"))
            .unwrap()
            .count(),
        1
    );
    fixture.state.control_mode.store(0, Ordering::SeqCst);
    let delivered = command(vec![
        "deliver-outcomes".into(),
        "--config".into(),
        config.clone().into_os_string(),
    ])
    .await;
    assert!(
        delivered.status.success(),
        "{}",
        String::from_utf8_lossy(&delivered.stderr)
    );
    let value: Value = serde_json::from_slice(&delivered.stdout).unwrap();
    assert_eq!(value["count"], 1);
    assert_eq!(
        fs::read_dir(fixture.root.join("state/outcomes"))
            .unwrap()
            .count(),
        0
    );
    assert_eq!(
        fs::read(fixture.root.join("state/fence.json")).unwrap(),
        marker
    );
    let still_blocked = command(vec![
        "serve".into(),
        "--config".into(),
        config.into_os_string(),
    ])
    .await;
    assert!(!still_blocked.status.success());
    assert!(
        String::from_utf8(still_blocked.stderr)
            .unwrap()
            .contains("GATEWAY_STATE_FENCED")
    );
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
    let output = std::env::var_os("PROJECT_ONE_GATEWAY_FENCE_EVIDENCE")
        .expect("explicit fence evidence location required");
    fs::write(output,serde_json::to_vec_pretty(&json!({"status":"passed_real_native_operator_fence","scope":"Actual private filesystem/owned suspended process/CLI/TLS; synthetic control/model replies, no hardware attestation or NVIDIA qualification.","assertions":["SIGSTOP retains exclusive state lock; fence refuses","SIGTERM permits scope-bound durable fencing","fenced serve refuses before inference","receipt overwrite refused","delivery-only retries existing metadata after control repair","metadata acknowledgment leaves permanent fence intact"],"generations":1})).unwrap()).unwrap();
}

struct TestGateway {
    app: Arc<Application>,
    origin: String,
    handle: axum_server::Handle<SocketAddr>,
    server: tokio::task::JoinHandle<()>,
}

impl TestGateway {
    async fn start(
        mut configuration: Configuration,
        root: PathBuf,
        http: &reqwest::Client,
    ) -> Self {
        use std::os::unix::fs::DirBuilderExt;
        fs::DirBuilder::new().mode(0o700).create(&root).unwrap();
        configuration.state_directory = root;
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        let SocketAddr::V4(address) = listener.local_addr().unwrap() else {
            panic!("IPv4 fixture")
        };
        configuration.listen_address = address;
        let app = Application::with_quota_clock(configuration.clone(), || QUOTA_TEST_NOW).unwrap();
        let handle = axum_server::Handle::new();
        let server_app = app.clone();
        let server_handle = handle.clone();
        let server = tokio::spawn(async move {
            crate::listener::serve(&configuration, server_app, listener, server_handle)
                .await
                .unwrap();
        });
        let origin = format!("https://{address}");
        tokio::time::timeout(Duration::from_secs(3), async {
            while http
                .get(format!("{origin}/health/live"))
                .send()
                .await
                .is_err()
            {
                tokio::time::sleep(Duration::from_millis(10)).await;
            }
        })
        .await
        .unwrap();
        Self {
            app,
            origin,
            handle,
            server,
        }
    }

    async fn stop(&mut self) {
        self.app.shutdown();
        self.handle.shutdown();
        tokio::time::timeout(Duration::from_secs(3), &mut self.server)
            .await
            .unwrap()
            .unwrap();
    }
}

impl Drop for TestGateway {
    fn drop(&mut self) {
        self.app.shutdown();
        self.handle.shutdown();
        self.server.abort();
    }
}

struct QuotaFleet {
    authority: TestGateway,
    peers: [TestGateway; 2],
    fixtures: [Fixture; 2],
}

impl QuotaFleet {
    async fn start(limits: Value) -> Self {
        let fixtures = [
            Fixture::with_quota(Some(json!({}))).await,
            Fixture::with_quota(Some(json!({}))).await,
        ];
        let root = &fixtures[0].root;
        let workspaces = fixtures.each_ref().map(|fixture| fixture.state.workspace);
        let keys = fixtures.each_ref().map(|fixture| {
            Uuid::parse_str(fixture.state.client_key.split('.').nth(1).unwrap()).unwrap()
        });
        let credentials = std::array::from_fn::<_, 2, _>(|index| {
            let path = root.join(format!("quota-peer-{index}"));
            let value = format!("{}{}", Uuid::new_v4().simple(), Uuid::new_v4().simple());
            fs::write(&path, value).unwrap();
            fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
            path
        });
        let mut config = fixtures[0].app().configuration.clone();
        config.gateway_id = Uuid::new_v4();
        config.quota_policy = Some(
            serde_json::from_value(json!({
                "tenants": [{"id": Uuid::new_v4(), "workspaces": workspaces,
                    "applications": [{"id": Uuid::new_v4(), "limits": limits, "keys": [
                        {"id": keys[0], "workspace_id": workspaces[0]},
                        {"id": keys[1], "workspace_id": workspaces[1]}
                    ]}]}]
            }))
            .unwrap(),
        );
        config.shared_quota = Some(serde_json::from_value(json!({
            "mode": "authority", "lease_seconds": 15, "maximum_leases": 16,
            "peers": [
                {"gateway_id": fixtures[0].state.gateway, "workspace_id": workspaces[0], "credential_file": credentials[0]},
                {"gateway_id": fixtures[1].state.gateway, "workspace_id": workspaces[1], "credential_file": credentials[1]}
            ]
        })).unwrap());
        config.listen_address.set_ip("10.0.0.7".parse().unwrap());
        assert_eq!(config.validate(), Ok(()));
        config.listen_address.set_ip("203.0.113.7".parse().unwrap());
        assert_eq!(
            config.validate(),
            Err("GATEWAY_SHARED_QUOTA_CONFIGURATION_INVALID")
        );
        config.listen_address.set_ip("127.0.0.1".parse().unwrap());
        let authority =
            TestGateway::start(config, root.join("authority"), &fixtures[0].client).await;
        let mut peers = Vec::new();
        for (index, fixture) in fixtures.iter().enumerate() {
            let mut config = fixture.app().configuration.clone();
            config.quota_policy = None;
            config.shared_quota = Some(
                serde_json::from_value(json!({
                    "mode": "client", "origin": authority.origin, "ca_file": root.join("ca.pem"),
                    "credential_file": credentials[index], "timeout_seconds": 2
                }))
                .unwrap(),
            );
            peers
                .push(TestGateway::start(config, fixture.root.join("peer"), &fixture.client).await);
        }
        Self {
            authority,
            peers: peers.try_into().ok().unwrap(),
            fixtures,
        }
    }

    fn chat(&self, peer: usize, stream: bool) -> reqwest::RequestBuilder {
        self.fixtures[peer].chat_at(&self.peers[peer].origin, stream)
    }
}

#[tokio::test]
async fn two_tls_gateways_share_a_daily_budget_and_fail_closed_without_authority() {
    let mut fleet = QuotaFleet::start(json!({"tokens":{"daily":32}})).await;
    let caller = &fleet.fixtures[0];
    let unauthorized = caller.client
        .post(format!("{}/internal/v1/quota", fleet.authority.origin))
        .bearer_auth(&caller.state.client_key)
        .json(&json!({"operation":"acquire", "workspace_id":caller.state.workspace, "key_id":Uuid::new_v4()}))
        .send().await.unwrap();
    assert_eq!(unauthorized.status(), StatusCode::UNAUTHORIZED);
    caller.state.backend_mode.store(4, Ordering::SeqCst);
    let pending = fleet.chat(0, true).send().await.unwrap();
    assert_eq!(pending.status(), StatusCode::OK);
    let rejected = fleet.chat(1, false).send().await.unwrap();
    assert_eq!(rejected.status(), StatusCode::TOO_MANY_REQUESTS);
    assert_eq!(
        rejected.headers()[header::RETRY_AFTER],
        (86_400 - QUOTA_TEST_NOW.rem_euclid(86_400)).to_string()
    );
    let error: Value = rejected.json().await.unwrap();
    assert_eq!(error["error"]["code"], "daily_token_budget_exceeded");
    assert_eq!(
        fleet.fixtures[1].state.generations.load(Ordering::SeqCst),
        0
    );
    caller.state.complete_stream.notify_one();
    assert!(pending.text().await.unwrap().ends_with("data: [DONE]\n\n"));
    let admitted = fleet.chat(1, false).send().await.unwrap();
    assert_eq!(admitted.status(), StatusCode::OK);
    assert_eq!(
        admitted.json::<Value>().await.unwrap()["usage"]["total_tokens"],
        9
    );

    fleet.authority.stop().await;
    let unavailable = fleet.chat(1, false).send().await.unwrap();
    assert_eq!(unavailable.status(), StatusCode::SERVICE_UNAVAILABLE);
    assert_eq!(
        unavailable.json::<Value>().await.unwrap()["error"]["code"],
        "quota_state_unavailable"
    );
    assert_eq!(
        fleet.fixtures[1].state.generations.load(Ordering::SeqCst),
        1
    );
}

#[tokio::test]
#[ignore = "run in release mode for loopback TLS latency measurements"]
async fn shared_quota_round_trip_benchmark() {
    let fleet = QuotaFleet::start(json!({})).await;
    let peer = &fleet.fixtures[0];
    let secret = fs::read_to_string(peer.root.join("quota-peer-0")).unwrap();
    let token = format!("q1.{}.{secret}", peer.state.gateway);
    let url = format!("{}/internal/v1/quota", fleet.authority.origin);
    let key = peer.state.client_key.split('.').nth(1).unwrap();
    let acquire = json!({"operation":"acquire","workspace_id":peer.state.workspace,"key_id":key});
    let mut samples = Vec::with_capacity(250);
    for index in 0..260 {
        let before = Instant::now();
        let grant: Value = peer
            .client
            .post(&url)
            .bearer_auth(&token)
            .json(&acquire)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap()
            .json()
            .await
            .unwrap();
        if index >= 10 {
            samples.push(before.elapsed());
        }
        peer.client
            .post(&url)
            .bearer_auth(&token)
            .json(&json!({"operation":"finish","lease_id":grant["lease_id"],"actual":null}))
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
    }
    samples.sort_unstable();
    println!(
        "{} TLS quota acquires; p50={} us; p95={} us; p99={} us",
        samples.len(),
        samples[125].as_micros(),
        samples[237].as_micros(),
        samples[247].as_micros()
    );
}
