//! Identified synthetic control responses exercise actual private TLS/HTTP, not Go authorization.
use crate::configuration::{Configuration, Limits};
use crate::control::{AdmissionRequest, Control, initialize_tls_provider};
use axum::extract::{Path as RoutePath, State};
use axum::http::{HeaderMap, StatusCode, header};
use axum::response::{IntoResponse, Response};
use axum::routing::post;
use axum::{Json, Router};
use serde_json::{Value, json};
use std::fs;
use std::net::{SocketAddr, SocketAddrV4, TcpListener};
use std::os::unix::fs::PermissionsExt;
use std::path::PathBuf;
use std::sync::Arc;
use std::sync::atomic::{AtomicUsize, Ordering};
use std::time::Duration;
use uuid::Uuid;

#[derive(Clone)]
struct SyntheticState {
    gateway: Uuid,
    node: Uuid,
    workspace: Uuid,
    calls: Arc<AtomicUsize>,
    mode: Arc<AtomicUsize>,
}

async fn synthetic_admission(
    State(state): State<SyntheticState>,
    RoutePath(gateway): RoutePath<Uuid>,
    headers: HeaderMap,
    Json(body): Json<Value>,
) -> Response {
    state.calls.fetch_add(1, Ordering::SeqCst);
    assert_eq!(gateway, state.gateway);
    assert_eq!(
        headers[header::AUTHORIZATION],
        "Bearer synthetic-gateway-credential"
    );
    assert!(body.get("messages").is_none());
    assert_eq!(body["api_key"], "synthetic-client-key");
    match state.mode.load(Ordering::SeqCst) {
        1 => return (StatusCode::UNAUTHORIZED, Json(json!({"code":"API_KEY_INVALID","detail":"synthetic private diagnostics must never escape"}))).into_response(),
        6 => return (StatusCode::FORBIDDEN, Json(json!({"code":"LICENSE_NEW_BUSINESS_UNAVAILABLE","detail":"synthetic customer data must never escape"}))).into_response(),
        2 => return (StatusCode::TEMPORARY_REDIRECT, [(header::LOCATION, format!("/gateway/v1/sites/default/cells/default/instances/{gateway}/admissions"))]).into_response(),
        4 => return ([(header::CONTENT_TYPE,"application/json")], "x".repeat(262_145)).into_response(),
        5 => tokio::time::sleep(Duration::from_secs(2)).await,
        _ => {}
    }
    let node = if state.mode.load(Ordering::SeqCst) == 3 {
        Uuid::new_v4()
    } else {
        state.node
    };
    Json(json!({"data":{"request_id":body["request_id"],"gateway_id":state.gateway,"node_id":node,"workspace_id":state.workspace,"binding":{"id":Uuid::new_v4(),"name":body["model"],"created":1,"deployment_id":Uuid::new_v4(),"deployment_revision":1,"backend_model":"synthetic-internal"},"maximum_context_tokens":2048,"maximum_request_seconds":90}})).into_response()
}

struct Fixture {
    root: PathBuf,
    configuration: Configuration,
    state: SyntheticState,
    handle: axum_server::Handle<SocketAddr>,
}

impl Drop for Fixture {
    fn drop(&mut self) {
        self.handle.shutdown();
        fs::remove_dir_all(&self.root).unwrap();
    }
}

impl Fixture {
    async fn new() -> Self {
        initialize_tls_provider();
        let root = std::env::temp_dir().join(format!("gateway-tls-{}", Uuid::new_v4()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        let root = root.canonicalize().unwrap();
        let generated = rcgen::generate_simple_self_signed(vec!["127.0.0.1".into()]).unwrap();
        let certificate = generated.cert.pem().into_bytes();
        let key = generated.signing_key.serialize_pem().into_bytes();
        for (name, bytes) in [
            ("ca.pem", certificate.clone()),
            ("certificate.pem", certificate.clone()),
            ("key.pem", key.clone()),
            ("credential", b"synthetic-gateway-credential\n".to_vec()),
        ] {
            fs::write(root.join(name), bytes).unwrap();
            fs::set_permissions(root.join(name), fs::Permissions::from_mode(0o600)).unwrap();
        }
        let listener = TcpListener::bind("127.0.0.1:0").unwrap();
        listener.set_nonblocking(true).unwrap();
        let SocketAddr::V4(address) = listener.local_addr().unwrap() else {
            panic!("explicit IPv4 fixture")
        };
        let gateway = Uuid::new_v4();
        let node = Uuid::new_v4();
        let state = SyntheticState {
            gateway,
            node,
            workspace: Uuid::new_v4(),
            calls: Arc::new(AtomicUsize::new(0)),
            mode: Arc::new(AtomicUsize::new(0)),
        };
        let router = Router::new()
            .route(
                "/gateway/v1/sites/default/cells/default/instances/{gateway}/admissions",
                post(synthetic_admission),
            )
            .with_state(state.clone());
        let tls = axum_server::tls_rustls::RustlsConfig::from_pem(certificate, key)
            .await
            .unwrap();
        let handle = axum_server::Handle::new();
        let server_handle = handle.clone();
        tokio::spawn(async move {
            axum_server::from_tcp_rustls(listener, tls)
                .unwrap()
                .handle(server_handle)
                .serve(router.into_make_service())
                .await
                .unwrap()
        });
        let configuration = Configuration {
            quota_policy: None,
            gateway_id: gateway,
            node_id: node,
            listen_address: SocketAddrV4::new(*address.ip(), 19443),
            backend_address: SocketAddrV4::new(*address.ip(), 19001),
            control_origin: format!("https://{address}"),
            site_id: "default".to_owned(),
            cell_id: "default".to_owned(),
            control_ca_file: root.join("ca.pem"),
            gateway_credential_file: root.join("credential"),
            tls_certificate_file: root.join("certificate.pem"),
            tls_private_key_file: root.join("key.pem"),
            state_directory: root.clone(),
            limits: Limits {
                connect_seconds: 1,
                control_seconds: 1,
                ..Limits::default()
            },
        };
        Self {
            root,
            configuration,
            state,
            handle,
        }
    }
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn actual_tls_authorization_has_no_cache_redirect_retry_or_diagnostic_disclosure() {
    let fixture = Fixture::new().await;
    let control = Control::new(&fixture.configuration).unwrap();
    let request = AdmissionRequest {
        request_id: Uuid::new_v4(),
        api_key: "synthetic-client-key",
        model: "public-alias",
        maximum_output_tokens: 16,
        stream: true,
    };
    let admitted = control.admit(&request).await.unwrap();
    assert_eq!(admitted.request_id, request.request_id);
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 1);
    fixture.state.mode.store(1, Ordering::SeqCst);
    assert!(matches!(
        control.admit(&request).await,
        Err("invalid_api_key")
    ));
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 2);
    fixture.state.mode.store(2, Ordering::SeqCst);
    assert!(matches!(
        control.admit(&request).await,
        Err("gateway_unavailable")
    ));
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 3);
    for mode in [3, 4] {
        fixture.state.mode.store(mode, Ordering::SeqCst);
        assert!(matches!(
            control.admit(&request).await,
            Err("gateway_unavailable")
        ));
    }
    fixture.state.mode.store(6, Ordering::SeqCst);
    assert!(matches!(
        control.admit(&request).await,
        Err("license_unavailable")
    ));
    let response = crate::errors::response("license_unavailable", Uuid::new_v4());
    assert_eq!(response.status(), StatusCode::FORBIDDEN);
    fixture.state.mode.store(5, Ordering::SeqCst);
    let started = std::time::Instant::now();
    assert!(matches!(
        control.admit(&request).await,
        Err("gateway_unavailable")
    ));
    assert!(started.elapsed() < Duration::from_secs(2));
}

#[tokio::test(flavor = "multi_thread", worker_threads = 2)]
async fn actual_untrusted_tls_ca_fails_before_sending_gateway_credential() {
    let mut fixture = Fixture::new().await;
    let wrong = rcgen::generate_simple_self_signed(vec!["127.0.0.1".into()]).unwrap();
    fs::write(fixture.root.join("wrong-ca.pem"), wrong.cert.pem()).unwrap();
    fs::set_permissions(
        fixture.root.join("wrong-ca.pem"),
        fs::Permissions::from_mode(0o600),
    )
    .unwrap();
    fixture.configuration.control_ca_file = fixture.root.join("wrong-ca.pem");
    let control = Control::new(&fixture.configuration).unwrap();
    let request = AdmissionRequest {
        request_id: Uuid::new_v4(),
        api_key: "synthetic-client-key",
        model: "public-alias",
        maximum_output_tokens: 16,
        stream: false,
    };
    assert!(matches!(
        control.admit(&request).await,
        Err("gateway_unavailable")
    ));
    assert_eq!(fixture.state.calls.load(Ordering::SeqCst), 0);
}
