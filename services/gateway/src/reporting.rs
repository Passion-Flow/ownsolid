use crate::admission::stopped;
use crate::configuration::Result;
use crate::routing::Application;
use std::sync::Arc;
use std::time::Duration;
use tokio::sync::watch;

pub async fn flush(application: &Application) -> Result<usize> {
    flush_spool(&application.control, &application.spool).await
}

pub async fn flush_spool(
    control: &crate::control::Control,
    spool: &crate::storage::Spool,
) -> Result<usize> {
    let mut delivered = 0;
    while let Some(outcome) = spool.next_pending()? {
        if let Err(code) = control.deliver_outcome(&outcome).await {
            if matches!(
                code,
                "GATEWAY_OUTCOME_ACKNOWLEDGMENT_INVALID"
                    | "GATEWAY_OUTCOME_REJECTED"
                    | "GATEWAY_OUTCOME_AUTHORITY_INVALIDATED"
            ) {
                spool.degrade();
            }
            return Err(code);
        }
        spool.acknowledge(&outcome)?;
        delivered += 1;
    }
    Ok(delivered)
}

pub async fn run(application: Arc<Application>, shutdown: watch::Receiver<bool>) {
    let mut interval = tokio::time::interval(Duration::from_secs(
        application.configuration.limits.outcome_retry_seconds,
    ));
    interval.set_missed_tick_behavior(tokio::time::MissedTickBehavior::Skip);
    loop {
        tokio::select! {
            biased;
            _=stopped(shutdown.clone())=>break,
            _=interval.tick()=>{let _=flush(&application).await;},
        }
    }
    let _ = tokio::time::timeout(
        Duration::from_secs(application.configuration.limits.shutdown_seconds),
        flush(&application),
    )
    .await;
}
