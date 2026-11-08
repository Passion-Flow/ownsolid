use crate::configuration::{Configuration, Limits, Result, certificate_file, secret_file};
use crate::control::initialize_tls_provider;
use crate::routing::{Application, router};
use axum_server::accept::Accept;
use std::future::{Future, Ready, ready};
use std::io;
use std::net::{SocketAddr, TcpListener};
use std::pin::Pin;
use std::sync::Arc;
use std::task::{Context, Poll};
use std::time::Duration;
use tokio::io::{AsyncRead, AsyncWrite, ReadBuf};
use tokio::net::TcpStream;
use tokio::sync::{OwnedSemaphorePermit, Semaphore};

const MAX_REQUEST_HEADERS: usize = 32;
const HTTP1_PROTOCOL: &[u8] = b"http/1.1";

struct Connection {
    stream: TcpStream,
    deadline: Pin<Box<tokio::time::Sleep>>,
    _permit: OwnedSemaphorePermit,
}
impl Connection {
    fn timed_out(&mut self, context: &mut Context<'_>) -> io::Result<()> {
        if self.deadline.as_mut().poll(context).is_ready() {
            return Err(io::Error::new(
                io::ErrorKind::TimedOut,
                "connection deadline exceeded",
            ));
        }
        Ok(())
    }
}
impl AsyncRead for Connection {
    fn poll_read(
        self: Pin<&mut Self>,
        context: &mut Context<'_>,
        buffer: &mut ReadBuf<'_>,
    ) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        this.timed_out(context)?;
        Pin::new(&mut this.stream).poll_read(context, buffer)
    }
}
impl AsyncWrite for Connection {
    fn poll_write(
        self: Pin<&mut Self>,
        context: &mut Context<'_>,
        bytes: &[u8],
    ) -> Poll<io::Result<usize>> {
        let this = self.get_mut();
        this.timed_out(context)?;
        Pin::new(&mut this.stream).poll_write(context, bytes)
    }
    fn poll_flush(self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<io::Result<()>> {
        let this = self.get_mut();
        this.timed_out(context)?;
        Pin::new(&mut this.stream).poll_flush(context)
    }
    fn poll_shutdown(self: Pin<&mut Self>, context: &mut Context<'_>) -> Poll<io::Result<()>> {
        Pin::new(&mut self.get_mut().stream).poll_shutdown(context)
    }
}

#[derive(Clone)]
struct ConnectionAcceptor {
    permits: Arc<Semaphore>,
    lifetime: Duration,
}
impl ConnectionAcceptor {
    fn new(limits: &Limits) -> Self {
        // One HTTP/1 request per connection; cover bounded headers/body/auth/queue/generation/terminal delivery.
        let seconds = limits.header_seconds
            + limits.idle_seconds * 2
            + limits.control_seconds * 2
            + limits.request_seconds;
        Self {
            permits: Arc::new(Semaphore::new(limits.maximum_connections as usize)),
            lifetime: Duration::from_secs(seconds)
                + Duration::from_millis(limits.queue_milliseconds),
        }
    }
}
impl<S> Accept<TcpStream, S> for ConnectionAcceptor {
    type Stream = Connection;
    type Service = S;
    type Future = Ready<io::Result<(Connection, S)>>;
    fn accept(&self, stream: TcpStream, service: S) -> Self::Future {
        let result = self
            .permits
            .clone()
            .try_acquire_owned()
            .map_err(|_| {
                io::Error::new(
                    io::ErrorKind::ConnectionRefused,
                    "connection capacity reached",
                )
            })
            .and_then(|permit| {
                stream.set_nodelay(true)?;
                Ok((
                    Connection {
                        stream,
                        deadline: Box::pin(tokio::time::sleep(self.lifetime)),
                        _permit: permit,
                    },
                    service,
                ))
            });
        ready(result)
    }
}

/// The listener's TLS configuration. The server speaks HTTP/1.1 only, so ALPN offers only
/// `http/1.1`: axum-server's default also offers `h2`, which clients such as curl and Go's
/// `net/http` then select, and the HTTP/1-only server drops the connection after the handshake.
async fn tls_configuration(
    configuration: &Configuration,
) -> Result<axum_server::tls_rustls::RustlsConfig> {
    let certificate = certificate_file(&configuration.tls_certificate_file)?;
    let key = secret_file(&configuration.tls_private_key_file)?;
    let defaults = axum_server::tls_rustls::RustlsConfig::from_pem(certificate, key)
        .await
        .map_err(|_| "GATEWAY_TLS_CONFIGURATION_INVALID")?;
    let mut server = (*defaults.get_inner()).clone();
    server.alpn_protocols = vec![HTTP1_PROTOCOL.to_vec()];
    Ok(axum_server::tls_rustls::RustlsConfig::from_config(
        Arc::new(server),
    ))
}

pub async fn serve(
    configuration: &Configuration,
    application: Arc<Application>,
    listener: TcpListener,
    handle: axum_server::Handle<SocketAddr>,
) -> Result<()> {
    initialize_tls_provider();
    if listener
        .local_addr()
        .map_err(|_| "GATEWAY_LISTENER_INVALID")?
        != SocketAddr::V4(configuration.listen_address)
    {
        return Err("GATEWAY_LISTENER_CONFIGURATION_MISMATCH");
    }
    listener
        .set_nonblocking(true)
        .map_err(|_| "GATEWAY_LISTENER_INVALID")?;
    let tls = tls_configuration(configuration).await?;
    let limits = &configuration.limits;
    let mut server = axum_server::from_tcp_rustls(listener, tls)
        .map_err(|_| "GATEWAY_LISTENER_INVALID")?
        .map(|acceptor| {
            acceptor
                .handshake_timeout(Duration::from_secs(limits.header_seconds))
                .acceptor(ConnectionAcceptor::new(limits))
        })
        .handle(handle)
        .http1_only();
    server
        .http_builder()
        .http1()
        .timer(hyper_util::rt::TokioTimer::new())
        .header_read_timeout(Duration::from_secs(limits.header_seconds))
        .max_buf_size(limits.header_bytes)
        .max_headers(MAX_REQUEST_HEADERS)
        .keep_alive(false);
    server
        .serve(router(application).into_make_service())
        .await
        .map_err(|_| "GATEWAY_SERVE_FAILED")
}
