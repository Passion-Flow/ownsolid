use crate::admission::{Execution, stopped};
use crate::backend::transport_error;
use crate::configuration::Result;
use crate::protocol::Usage;
use crate::streaming::Decoder;
use futures_util::StreamExt;
use std::time::Duration;
use tokio::sync::{OwnedSemaphorePermit, mpsc, watch};
use tokio::time::Instant;
use uuid::Uuid;

pub struct StreamWork {
    pub response: reqwest::Response,
    pub decoder: Decoder,
    pub execution: Execution,
    pub sender: mpsc::Sender<Vec<u8>>,
    pub shutdown: watch::Receiver<bool>,
    pub deadline: Instant,
    pub idle: Duration,
    pub request_id: Uuid,
    pub ingress: OwnedSemaphorePermit,
    pub active: OwnedSemaphorePermit,
}

async fn send(
    sender: &mpsc::Sender<Vec<u8>>,
    shutdown: watch::Receiver<bool>,
    deadline: Instant,
    idle: Duration,
    bytes: Vec<u8>,
) -> Result<()> {
    tokio::select! {
        biased;
        _=stopped(shutdown)=>Err("gateway_shutdown"),
        _=tokio::time::sleep_until(deadline.min(Instant::now()+idle))=>Err("client_cancelled"),
        result=sender.send(bytes)=>result.map_err(|_|"client_cancelled"),
    }
}

impl StreamWork {
    pub async fn run(self) {
        let Self {
            response,
            mut decoder,
            mut execution,
            sender,
            shutdown,
            deadline,
            idle,
            request_id,
            ingress: _ingress,
            active: _active,
        } = self;
        let mut stream = response.bytes_stream();
        let result: Result<(Usage, Vec<u8>)> = async {
            if let Some(bytes) = decoder.start() {
                send(&sender, shutdown.clone(), deadline, idle, bytes).await?;
            }
            loop {
                if deadline <= Instant::now() {
                    return Err("upstream_timeout");
                }
                let next=tokio::select! {
                    biased;
                    _=stopped(shutdown.clone())=>return Err("gateway_shutdown"),
                    _=sender.closed()=>return Err("client_cancelled"),
                    _=tokio::time::sleep_until(deadline.min(Instant::now()+idle))=>return Err("upstream_timeout"),
                    result=stream.next()=>result,
                };
                let Some(chunk)=next else{break;};
                let chunk=chunk.map_err(transport_error)?;
                let mut offset=0;
                while offset<chunk.len() {
                    let(used,event)=decoder.consume(&chunk[offset..])?;offset+=used;
                    if let Some(bytes)=event {
                        send(&sender,shutdown.clone(),deadline,idle,bytes).await?;
                        execution.output_started();
                    }
                }
            }
            decoder.finish()
        }.await;
        // Dropping the response stream before reporting prevents an incomplete backend connection lingering.
        drop(stream);
        let terminal = match result {
            Ok((usage, terminal)) => {
                execution.settle_tokens(&usage).await;
                match execution.success(usage) {
                    Ok(()) => terminal,
                    Err(code) => decoder.failure(code, request_id),
                }
            }
            Err(code) => {
                let code = execution.failure(code, None).err().unwrap_or(code);
                decoder.failure(code, request_id)
            }
        };
        let _ = send(&sender, shutdown, deadline, idle, terminal).await;
    }
}
