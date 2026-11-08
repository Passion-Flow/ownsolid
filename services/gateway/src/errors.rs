use axum::Json;
use axum::http::{StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde_json::{Value, json};
use uuid::Uuid;

pub fn document(code: &'static str, request_id: Uuid) -> Value {
    json!({"error":{"message":"The request could not be completed.","type":code,"param":null,"code":code},"request_id":request_id})
}

#[derive(Debug)]
pub(crate) struct Failure {
    pub(crate) code: &'static str,
    pub(crate) scope: Option<&'static str>,
    pub(crate) retry_after: Option<u64>,
}

impl From<&'static str> for Failure {
    fn from(code: &'static str) -> Self {
        Self {
            code,
            scope: None,
            retry_after: matches!(code, "queue_full" | "rate_limit_exceeded").then_some(1),
        }
    }
}

impl Failure {
    pub(crate) fn throttled(code: &'static str, scope: &'static str, retry_after: u64) -> Self {
        Self {
            code,
            scope: Some(scope),
            retry_after: Some(retry_after),
        }
    }
}

pub fn response(code: &'static str, request_id: Uuid) -> Response {
    respond(code.into(), request_id)
}

pub(crate) fn respond(failure: Failure, request_id: Uuid) -> Response {
    let code = failure.code;
    let status = match code {
        "invalid_api_key" | "key_expired" => StatusCode::UNAUTHORIZED,
        "permission_denied" | "license_unavailable" | "quota_scope_unconfigured" => {
            StatusCode::FORBIDDEN
        }
        "model_not_found" | "not_found" => StatusCode::NOT_FOUND,
        "method_not_allowed" => StatusCode::METHOD_NOT_ALLOWED,
        "queue_full"
        | "rate_limit_exceeded"
        | "request_rate_exceeded"
        | "concurrency_limit_exceeded"
        | "hourly_token_budget_exceeded"
        | "daily_token_budget_exceeded"
        | "monthly_token_budget_exceeded" => StatusCode::TOO_MANY_REQUESTS,
        "request_too_large" => StatusCode::PAYLOAD_TOO_LARGE,
        "invalid_request_error" | "unsupported_parameter" | "context_length_exceeded" => {
            StatusCode::BAD_REQUEST
        }
        "upstream_timeout" => StatusCode::GATEWAY_TIMEOUT,
        "upstream_response_invalid" => StatusCode::BAD_GATEWAY,
        _ => StatusCode::SERVICE_UNAVAILABLE,
    };
    let mut body = document(code, request_id);
    let message = match (code, failure.scope) {
        ("request_rate_exceeded", Some(scope)) =>
            Some(format!("The {scope} requests-per-second limit was reached. Reduce the request rate and retry.")),
        ("concurrency_limit_exceeded", Some(scope)) =>
            Some(format!("The {scope} concurrent request limit was reached. Retry when an active request finishes.")),
        (code, Some(scope)) => {
            let period = code.split('_').next().expect("budget error has a period");
            Some(format!("The {scope} {period} token budget cannot cover this request. Use a shorter prompt or lower output limit, or retry when budget becomes available."))
        }
        ("quota_scope_unconfigured", _) => Some("This API key has no quota assignment for its workspace. Ask the gateway operator to register it.".into()),
        ("quota_state_unavailable", _) => Some("The gateway quota state is unavailable. Ask the gateway operator to check quota connectivity, state storage and the system clock.".into()),
        _ => None,
    };
    if let Some(message) = message {
        body["error"]["message"] = message.into();
    }
    let mut response = (status, Json(body)).into_response();
    if let Some(seconds) = failure.retry_after {
        response
            .headers_mut()
            .insert(header::RETRY_AFTER, seconds.into());
    }
    protect(&mut response, request_id);
    response
}

pub fn protect(response: &mut Response, request_id: Uuid) {
    let headers = response.headers_mut();
    headers.insert(
        header::CACHE_CONTROL,
        header::HeaderValue::from_static("no-store"),
    );
    headers.insert(
        "x-content-type-options",
        header::HeaderValue::from_static("nosniff"),
    );
    headers.insert(
        "x-request-id",
        header::HeaderValue::from_str(&request_id.to_string()).expect("UUID header invariant"),
    );
}
