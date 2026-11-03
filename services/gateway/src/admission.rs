use crate::configuration::{Limits, Result};
use crate::outcome::{ErrorCode, Outcome, Status};
use crate::protocol::Usage;
use crate::storage::Reservation;
use std::sync::Arc;
use std::time::{Duration, Instant};
use tokio::sync::{OwnedSemaphorePermit, Semaphore, watch};
use uuid::Uuid;

pub struct Limiter {
    ingress: Arc<Semaphore>,
    active: Arc<Semaphore>,
    queue: Arc<Semaphore>,
    queue_wait: Duration,
}

pub async fn stopped(mut receiver: watch::Receiver<bool>) {
    let _ = receiver.wait_for(|value| *value).await;
}

impl Limiter {
    pub fn new(limits: &Limits) -> Result<Self> {
        limits.validate()?;
        Ok(Self {
            ingress: Arc::new(Semaphore::new(
                (limits.active_requests + limits.queued_requests) as usize,
            )),
            active: Arc::new(Semaphore::new(limits.active_requests as usize)),
            queue: Arc::new(Semaphore::new(limits.queued_requests as usize)),
            queue_wait: Duration::from_millis(limits.queue_milliseconds),
        })
    }

    /// Acquired before body collection or control RPC, so overload cannot allocate unbounded bodies.
    pub fn enter(&self) -> Result<OwnedSemaphorePermit> {
        self.ingress
            .clone()
            .try_acquire_owned()
            .map_err(|_| "queue_full")
    }

    pub async fn active(&self, shutdown: watch::Receiver<bool>) -> Result<OwnedSemaphorePermit> {
        if *shutdown.borrow() {
            return Err("gateway_shutdown");
        }
        if let Ok(permit) = self.active.clone().try_acquire_owned() {
            return Ok(permit);
        }
        let _queued = self
            .queue
            .clone()
            .try_acquire_owned()
            .map_err(|_| "queue_full")?;
        tokio::select! {
            biased;
            _ = stopped(shutdown) => Err("gateway_shutdown"),
            result = tokio::time::timeout(self.queue_wait,self.active.clone().acquire_owned()) =>
                result.map_err(|_|"queue_full")?.map_err(|_|"gateway_shutdown"),
        }
    }
}

/// Only created after a validated durable Go admission; drop records interrupted local execution.
pub struct Execution {
    reservation: Option<Reservation>,
    request_id: Uuid,
    started: Instant,
    output_started: bool,
}

impl Execution {
    pub fn new(reservation: Reservation, request_id: Uuid) -> Self {
        Self {
            reservation: Some(reservation),
            request_id,
            started: Instant::now(),
            output_started: false,
        }
    }
    pub fn output_started(&mut self) {
        self.output_started = true;
    }

    fn finish(
        &mut self,
        status: Status,
        error_code: Option<ErrorCode>,
        usage: Option<Usage>,
    ) -> Result<()> {
        let reservation = self.reservation.take().ok_or("usage_storage_unavailable")?;
        let outcome = Outcome {
            request_id: self.request_id,
            status,
            error_code,
            usage,
            elapsed_milliseconds: self
                .started
                .elapsed()
                .as_millis()
                .try_into()
                .unwrap_or(u64::MAX),
            output_started: self.output_started,
        };
        reservation
            .complete(&outcome)
            .map_err(|_| "usage_storage_unavailable")
    }

    pub fn success(&mut self, usage: Usage) -> Result<()> {
        self.finish(Status::Succeeded, None, Some(usage))
    }

    pub fn failure(&mut self, code: &str, usage: Option<Usage>) -> Result<()> {
        let (status, error) = match code {
            "client_cancelled" => (Status::ClientCancelled, ErrorCode::ClientCancelled),
            "context_length_exceeded" => (Status::UpstreamError, ErrorCode::ContextLengthExceeded),
            "upstream_timeout" => (Status::UpstreamTimeout, ErrorCode::UpstreamTimeout),
            "upstream_response_invalid" => {
                (Status::InvalidResponse, ErrorCode::UpstreamResponseInvalid)
            }
            "gateway_shutdown" => (Status::GatewayShutdown, ErrorCode::GatewayShutdown),
            "upstream_unavailable" => (Status::UpstreamError, ErrorCode::UpstreamUnavailable),
            _ => (Status::Incomplete, ErrorCode::GatewayInterrupted),
        };
        self.finish(status, Some(error), usage)
    }
}

impl Drop for Execution {
    fn drop(&mut self) {
        if self.reservation.is_some() {
            let _ = self.failure("client_cancelled", None);
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;
    fn limits() -> Limits {
        Limits {
            active_requests: 1,
            backend_idle_connections: 1,
            queued_requests: 1,
            queue_milliseconds: 20,
            ..Limits::default()
        }
    }
    #[tokio::test]
    async fn actual_semaphores_bound_ingress_queue_timeout_release_and_shutdown() {
        let limiter = Arc::new(Limiter::new(&limits()).unwrap());
        let first = limiter.enter().unwrap();
        let second = limiter.enter().unwrap();
        assert!(matches!(limiter.enter(), Err("queue_full")));
        drop(second);
        assert!(limiter.enter().is_ok());
        drop(first);
        let (tx, rx) = watch::channel(false);
        let active = limiter.active(rx.clone()).await.unwrap();
        assert!(matches!(
            limiter.active(rx.clone()).await,
            Err("queue_full")
        ));
        let queued_limiter = limiter.clone();
        let queued_rx = rx.clone();
        let task = tokio::spawn(async move { queued_limiter.active(queued_rx).await });
        tokio::task::yield_now().await;
        drop(active);
        let active = task.await.unwrap().unwrap();
        tx.send_replace(true);
        assert!(matches!(limiter.active(rx).await, Err("gateway_shutdown")));
        drop(active);
    }
}
