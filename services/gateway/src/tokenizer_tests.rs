//! Actual frozen tokenizer output with a declared context wrapper; not a vLLM API/GPU test.
use crate::configuration::Limits;
use crate::protocol::ChatRequest;
use serde::Deserialize;
use serde_json::Value;
use sha2::{Digest, Sha256};

#[derive(Deserialize)]
struct Case {
    id: String,
    client_request: Value,
    expected_context_overflow: bool,
    tokenization: Value,
}
#[derive(Deserialize)]
struct Evidence {
    source_kind: String,
    api_server_executed: bool,
    model_generation_executed: bool,
    cuda_execution: bool,
    image_manifest_digest: String,
    model_catalog_file_sha256: String,
    cases: Vec<Case>,
}

#[test]
fn actual_offline_tokenizer_bytes_drive_rust_context_admission() {
    let path = std::env::var_os("PROJECT_ONE_ACTUAL_TOKENIZER_EVIDENCE")
        .expect("explicit actual-tokenizer evidence required for this profile");
    let bytes = std::fs::read(path).unwrap();
    assert!(bytes.len() < 2 * 1024 * 1024);
    let evidence: Evidence = serde_json::from_slice(&bytes).unwrap();
    assert_eq!(
        evidence.source_kind,
        "actual_transformers_tokenizer_with_declared_context_wrapper"
    );
    assert!(
        !evidence.api_server_executed
            && !evidence.model_generation_executed
            && !evidence.cuda_execution
    );
    let recipe: Value = serde_json::from_str(include_str!(
        "../../../contracts/agent/v1/inference-recipe.json"
    ))
    .unwrap();
    assert_eq!(
        evidence.image_manifest_digest,
        recipe["image_manifest_digest"]
    );
    let expected = Sha256::digest(include_bytes!(
        "../../../contracts/agent/v1/model-catalog.json"
    ))
    .iter()
    .map(|byte| format!("{byte:02x}"))
    .collect::<String>();
    assert_eq!(evidence.model_catalog_file_sha256, expected);
    assert_eq!(evidence.cases.len(), 3);
    let limits = Limits {
        maximum_context_tokens: recipe["maximum_context"]
            .as_u64()
            .unwrap()
            .try_into()
            .unwrap(),
        request_body_bytes: 1_048_576,
        ..Limits::default()
    };
    limits.validate().unwrap();
    for case in evidence.cases {
        let request =
            ChatRequest::parse(&serde_json::to_vec(&case.client_request).unwrap(), &limits)
                .unwrap();
        let actual = request
            .validate_tokenization(&serde_json::to_vec(&case.tokenization).unwrap(), &limits);
        if case.expected_context_overflow {
            assert_eq!(actual, Err("context_length_exceeded"), "{}", case.id)
        } else {
            assert_eq!(
                actual,
                Some(case.tokenization["count"].as_u64().unwrap() as u32)
                    .ok_or("upstream_response_invalid"),
                "{}",
                case.id
            )
        }
    }
}
