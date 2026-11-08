use projectone_gateway::configuration::{Configuration, Result};
use projectone_gateway::{
    control::Control,
    storage::{Spool, validate_receipt_path, write_receipt},
};
use projectone_gateway::{listener, reporting, routing::Application};
use std::net::TcpListener;
use std::path::PathBuf;
use std::time::Duration;

fn main() {
    if let Err(code) = execute() {
        eprintln!(
            "{}",
            serde_json::json!({"event":"gateway_failed","code":code})
        );
        std::process::exit(1);
    }
}

fn execute() -> Result<()> {
    let mut arguments = std::env::args_os().skip(1).collect::<Vec<_>>();
    if arguments.len() == 1 && arguments[0] == "--version" {
        println!("projectone-gateway {}", env!("CARGO_PKG_VERSION"));
        return Ok(());
    }
    if arguments.len() == 1 && arguments[0] == "--help" {
        println!(
            "projectone-gateway serve|inspect|deliver-outcomes --config <private-json> [--env-file <private-env>]\nprojectone-gateway fence --config <private-json> --challenge <uuid> --receipt-file <new-private-path> [--env-file <private-env>]"
        );
        return Ok(());
    }
    let mut environment = None;
    if let Some(index) = arguments.iter().position(|value| value == "--env-file") {
        if index < 3 || index + 2 != arguments.len() {
            return Err("GATEWAY_USAGE_INVALID");
        }
        environment = Some(PathBuf::from(&arguments[index + 1]));
        arguments.truncate(index);
    }
    let operation = arguments
        .first()
        .and_then(|value| value.to_str())
        .ok_or("GATEWAY_USAGE_INVALID")?;
    if !matches!(
        operation,
        "serve" | "inspect" | "deliver-outcomes" | "fence"
    ) || arguments.get(1).is_none_or(|value| value != "--config")
        || arguments.len() != if operation == "fence" { 7 } else { 3 }
    {
        return Err("GATEWAY_USAGE_INVALID");
    }
    let configuration = Configuration::load_with_environment(
        &PathBuf::from(&arguments[2]),
        environment.as_deref(),
    )?;
    if operation == "inspect" {
        let spool = Spool::open_for_delivery(&configuration)?;
        println!("{}", spool.inspection()?);
        return Ok(());
    }
    if operation == "fence" {
        if arguments[3] != "--challenge" || arguments[5] != "--receipt-file" {
            return Err("GATEWAY_USAGE_INVALID");
        }
        let challenge_text = arguments[4].to_str().ok_or("GATEWAY_CHALLENGE_INVALID")?;
        let challenge =
            uuid::Uuid::parse_str(challenge_text).map_err(|_| "GATEWAY_CHALLENGE_INVALID")?;
        if challenge.is_nil() || challenge.to_string() != challenge_text {
            return Err("GATEWAY_CHALLENGE_INVALID");
        }
        let path = PathBuf::from(&arguments[6]);
        if path.starts_with(&configuration.state_directory) {
            return Err("GATEWAY_RECEIPT_PATH_INVALID");
        }
        validate_receipt_path(&path)?;
        let spool = Spool::open_for_delivery(&configuration)?;
        let receipt = spool.fence(challenge)?;
        write_receipt(&path, &receipt)?;
        println!(
            "{}",
            serde_json::json!({"event":"gateway_fenced","gateway_id":configuration.gateway_id,"receipt_written":true})
        );
        return Ok(());
    }
    let runtime = tokio::runtime::Builder::new_multi_thread()
        .worker_threads(configuration.limits.runtime_threads)
        .max_blocking_threads(configuration.limits.blocking_threads)
        .thread_name("projectone-gateway")
        .enable_all()
        .build()
        .map_err(|_| "GATEWAY_RUNTIME_FAILED")?;
    if operation == "deliver-outcomes" {
        let spool = Spool::open_for_delivery(&configuration)?;
        let control = Control::new(&configuration)?;
        let delivered = runtime.block_on(async {
            tokio::time::timeout(
                Duration::from_secs(configuration.limits.operator_seconds),
                reporting::flush_spool(&control, &spool),
            )
            .await
            .map_err(|_| "GATEWAY_OPERATOR_TIMEOUT")?
        })?;
        println!(
            "{}",
            serde_json::json!({"event":"gateway_outcomes_delivered","count":delivered})
        );
        Ok(())
    } else {
        runtime.block_on(run(configuration))
    }
}

async fn run(configuration: Configuration) -> Result<()> {
    let listener = TcpListener::bind(configuration.listen_address)
        .map_err(|_| "GATEWAY_LISTENER_BIND_FAILED")?;
    let application = Application::new(configuration.clone())?;
    let handle = axum_server::Handle::new();
    let shutdown_handle = handle.clone();
    let shutdown_app = application.clone();
    let mut terminate = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::terminate())
        .map_err(|_| "GATEWAY_SIGNAL_FAILED")?;
    let mut hangup = tokio::signal::unix::signal(tokio::signal::unix::SignalKind::hangup())
        .map_err(|_| "GATEWAY_SIGNAL_FAILED")?;
    let tls = listener::tls_configuration(&configuration).await?;
    let reload_tls = tls.clone();
    let reload_app = application.clone();
    let reloader = tokio::spawn(async move {
        while hangup.recv().await.is_some() {
            let event = match listener::tls_configuration(&reload_app.configuration).await {
                Ok(candidate) => {
                    reload_tls.reload_from_config(candidate.get_inner());
                    serde_json::json!({"event":"gateway_tls_reloaded"})
                }
                Err(code) => serde_json::json!({"event":"gateway_tls_reload_failed","code":code}),
            };
            eprintln!("{event}");
        }
    });
    let signal = tokio::spawn(async move {
        tokio::select! {_=tokio::signal::ctrl_c()=>{},_=terminate.recv()=>{}}
        shutdown_app.shutdown();
        shutdown_handle.graceful_shutdown(Some(Duration::from_secs(
            shutdown_app.configuration.limits.shutdown_seconds,
        )));
    });
    let (reporter_stop, reporter_receiver) = tokio::sync::watch::channel(false);
    let reporter = tokio::spawn(reporting::run(application.clone(), reporter_receiver));
    let result = listener::serve(&configuration, application.clone(), listener, handle, tls).await;
    reloader.abort();
    let _ = reloader.await;
    application.shutdown();
    application.finish_workers().await;
    reporter_stop.send_replace(true);
    let _ = reporter.await;
    signal.abort();
    let _ = signal.await;
    result
}
