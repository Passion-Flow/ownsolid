use crate::configuration::{Configuration, Limits, Result};
use crate::protocol::ChatRequest;
use futures_util::StreamExt;
use reqwest::{Response, StatusCode, header};
use serde::Deserialize;
use std::time::Duration;

/// One operator-configured colocated backend. No request data can select a destination.
pub struct Backend {
    client: reqwest::Client,
    origin: String,
}

pub(crate) fn transport_error(error: reqwest::Error) -> &'static str {
    if error.is_timeout() {
        "upstream_timeout"
    } else {
        "upstream_unavailable"
    }
}

pub(crate) fn response_type(response: &Response, expected: &str) -> Result<()> {
    if response.status() != StatusCode::OK {
        return Err("upstream_unavailable");
    }
    if response
        .headers()
        .get_all(header::CONTENT_TYPE)
        .iter()
        .count()
        != 1
        || response
            .headers()
            .get(header::CONTENT_TYPE)
            .and_then(|value| value.to_str().ok())
            .and_then(|value| value.split(';').next())
            .map(str::trim)
            != Some(expected)
        || response.headers().contains_key(header::CONTENT_ENCODING)
    {
        return Err("upstream_response_invalid");
    }
    Ok(())
}

pub(crate) async fn bounded_json(response: Response, maximum: u64) -> Result<Vec<u8>> {
    response_type(&response, "application/json")?;
    if response
        .content_length()
        .is_some_and(|length| length > maximum)
    {
        return Err("upstream_response_invalid");
    }
    let mut stream = response.bytes_stream();
    let mut bytes = Vec::new();
    while let Some(chunk) = stream.next().await {
        let chunk = chunk.map_err(transport_error)?;
        if bytes.len() as u64 + chunk.len() as u64 > maximum {
            return Err("upstream_response_invalid");
        }
        bytes.extend_from_slice(&chunk);
    }
    Ok(bytes)
}

impl Backend {
    pub fn new(configuration: &Configuration) -> Result<Self> {
        configuration.validate()?;
        crate::control::initialize_tls_provider();
        let limits = &configuration.limits;
        let client = reqwest::Client::builder()
            .http1_only()
            .no_proxy()
            .redirect(reqwest::redirect::Policy::none())
            .retry(reqwest::retry::never())
            .connect_timeout(Duration::from_secs(limits.connect_seconds))
            .read_timeout(Duration::from_secs(limits.idle_seconds))
            .timeout(Duration::from_secs(limits.request_seconds))
            .pool_max_idle_per_host(limits.backend_idle_connections)
            .build()
            .map_err(|_| "GATEWAY_BACKEND_CLIENT_INVALID")?;
        Ok(Self {
            client,
            origin: format!("http://{}", configuration.backend_address),
        })
    }

    pub async fn verify_model(&self, expected_model: &str, limits: &Limits) -> Result<()> {
        #[derive(Deserialize)]
        struct Model {
            id: String,
            object: String,
        }
        #[derive(Deserialize)]
        struct Models {
            object: String,
            data: Vec<Model>,
        }
        let response = self
            .client
            .get(format!("{}/v1/models", self.origin))
            .send()
            .await
            .map_err(transport_error)?;
        let bytes = bounded_json(response, limits.response_bytes).await?;
        let value: Models =
            serde_json::from_slice(&bytes).map_err(|_| "upstream_response_invalid")?;
        if value.object != "list"
            || value.data.len() != 1
            || value.data[0].object != "model"
            || value.data[0].id != expected_model
        {
            return Err("upstream_response_invalid");
        }
        Ok(())
    }

    pub async fn tokenize(
        &self,
        request: &ChatRequest,
        model: &str,
        limits: &Limits,
    ) -> Result<u32> {
        let response = self
            .client
            .post(format!("{}/tokenize", self.origin))
            .json(&request.tokenize_payload(model))
            .send()
            .await
            .map_err(transport_error)?;
        let bytes = bounded_json(response, limits.response_bytes).await?;
        request.validate_tokenization(&bytes, limits)
    }

    pub async fn completion(&self, request: &ChatRequest, model: &str) -> Result<Response> {
        let response = self
            .client
            .post(format!("{}/v1/chat/completions", self.origin))
            .json(&request.completion_payload(model))
            .send()
            .await
            .map_err(transport_error)?;
        response_type(
            &response,
            if request.stream {
                "text/event-stream"
            } else {
                "application/json"
            },
        )?;
        Ok(response)
    }
}
