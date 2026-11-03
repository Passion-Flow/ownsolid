use axum::Json;
use axum::http::{StatusCode, header};
use axum::response::{IntoResponse, Response};
use serde_json::{Value, json};
use uuid::Uuid;

pub fn document(code: &'static str, request_id: Uuid) -> Value {
    json!({"error":{"message":"The request could not be completed.","type":code,"param":null,"code":code},"request_id":request_id})
}

pub fn response(code: &'static str, request_id: Uuid) -> Response {
    let status = match code {
        "invalid_api_key" | "key_expired" => StatusCode::UNAUTHORIZED,
        "permission_denied" | "license_unavailable" => StatusCode::FORBIDDEN,
        "model_not_found" | "not_found" => StatusCode::NOT_FOUND,
        "method_not_allowed" => StatusCode::METHOD_NOT_ALLOWED,
        "queue_full" | "rate_limit_exceeded" => StatusCode::TOO_MANY_REQUESTS,
        "request_too_large" => StatusCode::PAYLOAD_TOO_LARGE,
        "invalid_request_error" | "unsupported_parameter" | "context_length_exceeded" => {
            StatusCode::BAD_REQUEST
        }
        "upstream_timeout" => StatusCode::GATEWAY_TIMEOUT,
        "upstream_response_invalid" => StatusCode::BAD_GATEWAY,
        _ => StatusCode::SERVICE_UNAVAILABLE,
    };
    let mut response = (status, Json(document(code, request_id))).into_response();
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
