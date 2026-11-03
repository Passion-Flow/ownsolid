use crate::configuration::{MAX_CONTEXT_TOKENS, Result};
use crate::protocol::Usage;
use serde::{Deserialize, Serialize};
use sha2::{Digest, Sha256};
use uuid::Uuid;

const MAX_ELAPSED_MILLISECONDS: u64 = 3_600_000;
const MAX_OUTPUT_TOKENS: u32 = 512;

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum Status {
    Succeeded,
    ClientCancelled,
    UpstreamTimeout,
    UpstreamError,
    InvalidResponse,
    GatewayShutdown,
    UsageStorageFailed,
    Incomplete,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq, Serialize, Deserialize)]
#[serde(rename_all = "snake_case")]
pub enum ErrorCode {
    ClientCancelled,
    ContextLengthExceeded,
    UpstreamUnavailable,
    UpstreamTimeout,
    UpstreamResponseInvalid,
    GatewayUnavailable,
    GatewayShutdown,
    UsageStorageUnavailable,
    GatewayInterrupted,
}

/// Metadata only: prompts, output content, API keys and gateway credentials have no field here.
#[derive(Clone, Serialize, Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Outcome {
    pub request_id: Uuid,
    pub status: Status,
    #[serde(deserialize_with = "required_nullable")]
    pub error_code: Option<ErrorCode>,
    #[serde(deserialize_with = "strict_usage")]
    pub usage: Option<Usage>,
    pub elapsed_milliseconds: u64,
    pub output_started: bool,
}

fn required_nullable<'de, T: Deserialize<'de>, D: serde::Deserializer<'de>>(
    decoder: D,
) -> std::result::Result<Option<T>, D::Error> {
    Option::<T>::deserialize(decoder)
}
fn strict_usage<'de, D: serde::Deserializer<'de>>(
    decoder: D,
) -> std::result::Result<Option<Usage>, D::Error> {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct StrictUsage {
        prompt_tokens: u32,
        completion_tokens: u32,
        total_tokens: u32,
    }
    Ok(
        Option::<StrictUsage>::deserialize(decoder)?.map(|value| Usage {
            prompt_tokens: value.prompt_tokens,
            completion_tokens: value.completion_tokens,
            total_tokens: value.total_tokens,
        }),
    )
}

impl Outcome {
    pub fn validate(&self) -> Result<()> {
        if self.request_id.is_nil()
            || self.elapsed_milliseconds > MAX_ELAPSED_MILLISECONDS
            || (self.status == Status::Succeeded
                && (self.error_code.is_some() || self.usage.is_none()))
            || (self.status != Status::Succeeded && self.error_code.is_none())
        {
            return Err("GATEWAY_OUTCOME_INVALID");
        }
        if let Some(usage) = &self.usage
            && (usage.prompt_tokens == 0
                || usage.total_tokens > MAX_CONTEXT_TOKENS
                || usage.completion_tokens > MAX_OUTPUT_TOKENS
                || usage.prompt_tokens.checked_add(usage.completion_tokens)
                    != Some(usage.total_tokens))
        {
            return Err("GATEWAY_OUTCOME_INVALID");
        }
        Ok(())
    }

    pub fn canonical_bytes(&self) -> Result<Vec<u8>> {
        self.validate()?;
        serde_json::to_vec(self).map_err(|_| "GATEWAY_OUTCOME_INVALID")
    }

    pub fn digest(&self) -> Result<String> {
        Ok(Sha256::digest(self.canonical_bytes()?)
            .iter()
            .map(|byte| format!("{byte:02x}"))
            .collect())
    }
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
pub struct Acknowledgment {
    pub request_id: Uuid,
    pub canonical_sha256: String,
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn success_requires_actual_consistent_usage_and_unknown_is_never_zero() {
        let mut outcome = Outcome {
            request_id: Uuid::new_v4(),
            status: Status::Succeeded,
            error_code: None,
            usage: None,
            elapsed_milliseconds: 1,
            output_started: true,
        };
        assert_eq!(outcome.validate(), Err("GATEWAY_OUTCOME_INVALID"));
        outcome.usage = Some(Usage {
            prompt_tokens: 7,
            completion_tokens: 2,
            total_tokens: 9,
        });
        assert!(outcome.validate().is_ok());
        outcome.usage.as_mut().unwrap().total_tokens = 10;
        assert!(outcome.validate().is_err());
        outcome.status = Status::ClientCancelled;
        outcome.error_code = Some(ErrorCode::ClientCancelled);
        outcome.usage = None;
        assert!(outcome.validate().is_ok());
        let value = serde_json::to_value(outcome).unwrap();
        assert!(value["usage"].is_null());
    }

    #[test]
    fn wire_order_nulls_and_metadata_hash_are_stable_and_reject_unknown_content() {
        let outcome = Outcome {
            request_id: Uuid::parse_str("00000000-0000-0000-0000-000000000001").unwrap(),
            status: Status::Incomplete,
            error_code: Some(ErrorCode::GatewayInterrupted),
            usage: None,
            elapsed_milliseconds: 0,
            output_started: false,
        };
        let expected=br#"{"request_id":"00000000-0000-0000-0000-000000000001","status":"incomplete","error_code":"gateway_interrupted","usage":null,"elapsed_milliseconds":0,"output_started":false}"#;
        assert_eq!(outcome.canonical_bytes().unwrap(), expected);
        let mut changed = outcome.clone();
        changed.output_started = true;
        assert_ne!(outcome.digest().unwrap(), changed.digest().unwrap());
        let mut value = serde_json::to_value(outcome).unwrap();
        value["prompt"] = serde_json::json!("must not persist");
        assert!(serde_json::from_value::<Outcome>(value).is_err());
    }

    #[test]
    fn metadata_requires_explicit_null_fields_and_rejects_nested_content() {
        let mut value = serde_json::json!({"request_id":Uuid::new_v4(),"status":"succeeded","error_code":null,"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"elapsed_milliseconds":1,"output_started":true});
        assert!(serde_json::from_value::<Outcome>(value.clone()).is_ok());
        value["usage"]["prompt"] = serde_json::json!("must not persist");
        assert!(serde_json::from_value::<Outcome>(value.clone()).is_err());
        value["usage"].as_object_mut().unwrap().remove("prompt");
        for key in ["usage", "error_code"] {
            let mut missing = value.clone();
            missing.as_object_mut().unwrap().remove(key);
            assert!(serde_json::from_value::<Outcome>(missing).is_err());
        }
    }
}
