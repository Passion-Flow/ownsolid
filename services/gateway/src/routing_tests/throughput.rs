use super::{Fixture, Limits, Status, TestGateway};
use axum::http::header;
use futures_util::{StreamExt, stream};
use serde::Serialize;
use serde_json::{Value, json};
use std::time::{Duration, Instant};

const REQUESTS: usize = 256;
const WARMUP_REQUESTS: usize = 32;
const CONCURRENCIES: [usize; 2] = [1, 8];
const CLIENT_DEADLINE: Duration = Duration::from_secs(30);

#[derive(Serialize)]
struct Measurement {
    requests_per_second: f64,
    p50_ms: f64,
    p95_ms: f64,
    p99_ms: f64,
}

impl Measurement {
    fn new(mut samples: Vec<Duration>, elapsed: Duration) -> Self {
        samples.sort_unstable();
        let percentile = |percent: usize| {
            let rank = (samples.len() * percent).div_ceil(100);
            samples[rank - 1].as_secs_f64() * 1_000.0
        };
        Self {
            requests_per_second: samples.len() as f64 / elapsed.as_secs_f64(),
            p50_ms: percentile(50),
            p95_ms: percentile(95),
            p99_ms: percentile(99),
        }
    }
}

async fn measure(
    request: &reqwest::RequestBuilder,
    expected_model: &str,
    concurrency: usize,
    count: usize,
) -> Measurement {
    let start = Instant::now();
    let samples = stream::iter(0..count)
        .map(|_| async {
            let sent = Instant::now();
            let response = request
                .try_clone()
                .unwrap()
                .send()
                .await
                .unwrap()
                .error_for_status()
                .unwrap();
            let bytes = response.bytes().await.unwrap();
            let latency = sent.elapsed();
            let value: Value = serde_json::from_slice(&bytes).unwrap();
            assert_eq!(value["model"], expected_model);
            assert_eq!(value["usage"]["total_tokens"], 9);
            latency
        })
        .buffer_unordered(concurrency)
        .collect()
        .await;
    Measurement::new(samples, start.elapsed())
}

#[tokio::test(flavor = "multi_thread", worker_threads = 4)]
#[ignore = "run in release mode for synthetic route throughput and latency measurements"]
async fn chat_route_throughput_benchmark() {
    let fixture = Fixture::new().await;
    let mut config = fixture.app().configuration.clone();
    let capacity = *CONCURRENCIES.last().unwrap();
    config.limits = Limits {
        active_requests: capacity.try_into().unwrap(),
        queued_requests: capacity.try_into().unwrap(),
        control_idle_connections: capacity,
        backend_idle_connections: capacity,
        ..Limits::default()
    };
    let backend_origin = format!("http://{}", config.backend_address);
    let gateway = TestGateway::start(config, fixture.root.join("benchmark"), &fixture.client).await;

    let chat = json!({
        "model": "public-alias",
        "messages": [{"role": "user", "content": "Synthetic benchmark prompt"}],
        "max_tokens": 16,
        "stream": false,
    });
    let mut backend_chat = chat.clone();
    backend_chat["model"] = json!("synthetic-internal");
    backend_chat["chat_template_kwargs"] = json!({"enable_thinking": false});
    let direct = fixture
        .client
        .post(format!("{backend_origin}/v1/chat/completions"))
        .json(&backend_chat)
        .header(header::CONNECTION, "close")
        .timeout(CLIENT_DEADLINE);
    let proxied = fixture
        .chat_at(&gateway.origin, false)
        .json(&chat)
        .header(header::CONNECTION, "close")
        .timeout(CLIENT_DEADLINE);

    for concurrency in CONCURRENCIES {
        measure(&direct, "synthetic-internal", concurrency, WARMUP_REQUESTS).await;
        measure(&proxied, "public-alias", concurrency, WARMUP_REQUESTS).await;
        let baseline = measure(&direct, "synthetic-internal", concurrency, REQUESTS).await;
        let routed = measure(&proxied, "public-alias", concurrency, REQUESTS).await;
        assert_eq!(
            crate::reporting::flush(&gateway.app).await.unwrap(),
            WARMUP_REQUESTS + REQUESTS
        );
        assert!(gateway.app.spool.next_pending().unwrap().is_none());
        assert!(
            fixture
                .state
                .outcomes
                .lock()
                .unwrap()
                .iter()
                .all(|outcome| outcome.status == Status::Succeeded)
        );
        println!(
            "{}",
            json!({
                "profile": "synthetic-control-and-backend",
                "concurrency": concurrency,
                "requests_per_path": REQUESTS,
                "direct_http": baseline,
                "gateway_tls": routed,
                "added_latency_ms": {
                    "p50": routed.p50_ms - baseline.p50_ms,
                    "p95": routed.p95_ms - baseline.p95_ms,
                    "p99": routed.p99_ms - baseline.p99_ms,
                },
            })
        );
    }
}
