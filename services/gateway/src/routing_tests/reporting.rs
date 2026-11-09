use super::{Fixture, TestGateway, wait_until};
use std::sync::atomic::Ordering;
use std::time::Duration;
use tokio::sync::watch;

#[tokio::test]
async fn reporter_wakes_on_publication_but_respects_outage_backoff() {
    let fixture = Fixture::new().await;
    let mut configuration = fixture.app().configuration.clone();
    configuration.limits.outcome_retry_seconds = 30;
    let mut gateway = TestGateway::start(
        configuration,
        fixture.root.join("reporter"),
        &fixture.client,
    )
    .await;
    let complete = || async {
        let response = fixture
            .chat_at(&gateway.origin, false)
            .send()
            .await
            .unwrap()
            .error_for_status()
            .unwrap();
        let body: serde_json::Value = response.json().await.unwrap();
        assert_eq!(body["usage"]["total_tokens"], 9);
    };

    // A record published before the reporter starts must still be delivered.
    complete().await;
    let (stop, receiver) = watch::channel(false);
    let reporter = tokio::spawn(crate::reporting::run(gateway.app.clone(), receiver));
    wait_until(|| gateway.app.spool.next_pending().unwrap().is_none()).await;
    assert_eq!(fixture.state.outcomes.lock().unwrap().len(), 1);

    complete().await;
    wait_until(|| gateway.app.spool.next_pending().unwrap().is_none()).await;
    assert_eq!(fixture.state.outcomes.lock().unwrap().len(), 2);

    // Control still admits calls, but cannot acknowledge their outcomes.
    fixture.state.control_mode.store(6, Ordering::SeqCst);
    complete().await;
    let attempts = &fixture.state.outcome_attempts;
    wait_until(|| attempts.load(Ordering::SeqCst) == 3).await;
    complete().await;
    let bypass = tokio::time::timeout(Duration::from_millis(250), async {
        while attempts.load(Ordering::SeqCst) == 3 {
            tokio::time::sleep(Duration::from_millis(10)).await;
        }
    })
    .await;
    assert!(bypass.is_err(), "publication bypassed the retry delay");
    assert!(gateway.app.spool.next_pending().unwrap().is_some());

    fixture.state.control_mode.store(0, Ordering::SeqCst);
    stop.send_replace(true);
    tokio::time::timeout(Duration::from_secs(3), reporter)
        .await
        .unwrap()
        .unwrap();
    assert!(gateway.app.spool.next_pending().unwrap().is_none());
    assert_eq!(fixture.state.outcomes.lock().unwrap().len(), 4);
    assert_eq!(attempts.load(Ordering::SeqCst), 5);
    gateway.stop().await;
}
