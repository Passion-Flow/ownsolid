use crate::configuration::{Limits, Result};
use serde::{Deserialize, Serialize};
use serde_json::{Value, json};

const MAX_ALIAS_BYTES: usize = 64;

#[derive(Clone, Deserialize, Serialize)]
#[serde(rename_all = "lowercase")]
pub enum Role {
    System,
    User,
    Assistant,
}

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Message {
    pub role: Role,
    pub content: String,
}

#[derive(Default, Deserialize)]
#[serde(deny_unknown_fields)]
struct StreamOptions {
    #[serde(default)]
    include_usage: bool,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct ChatInput {
    model: String,
    messages: Vec<Message>,
    max_tokens: Option<u32>,
    max_completion_tokens: Option<u32>,
    temperature: Option<f64>,
    n: Option<u8>,
    #[serde(default)]
    stream: bool,
    stream_options: Option<StreamOptions>,
}

pub struct ChatRequest {
    pub alias: String,
    pub messages: Vec<Message>,
    pub maximum_tokens: u32,
    pub temperature: f64,
    pub stream: bool,
    pub include_usage: bool,
}

pub fn valid_alias(value: &str) -> bool {
    !value.is_empty()
        && value.len() <= MAX_ALIAS_BYTES
        && value.as_bytes()[0].is_ascii_lowercase()
        && value.bytes().all(|value| {
            value.is_ascii_lowercase() || value.is_ascii_digit() || b".-".contains(&value)
        })
}

impl ChatRequest {
    pub fn parse(bytes: &[u8], limits: &Limits) -> Result<Self> {
        if bytes.len() as u64 > limits.request_body_bytes {
            return Err("request_too_large");
        }
        Self::validate(parse_input(bytes)?, limits)
    }

    /// Translate the foreground string-input Responses subset to text generation.
    pub fn parse_responses(bytes: &[u8], limits: &Limits) -> Result<Self> {
        #[derive(Deserialize)]
        #[serde(deny_unknown_fields)]
        struct Input {
            model: String,
            input: String,
            instructions: Option<String>,
            max_output_tokens: Option<u32>,
            temperature: Option<f64>,
            #[serde(default)]
            stream: bool,
            #[serde(default)]
            store: bool,
            #[serde(default)]
            background: bool,
        }
        if bytes.len() as u64 > limits.request_body_bytes {
            return Err("request_too_large");
        }
        let input: Input = parse_input(bytes)?;
        if input.store || input.background {
            return Err("unsupported_parameter");
        }
        let mut messages = Vec::with_capacity(2);
        if let Some(content) = input.instructions {
            messages.push(Message {
                role: Role::System,
                content,
            });
        }
        messages.push(Message {
            role: Role::User,
            content: input.input,
        });
        Self::validate(
            ChatInput {
                model: input.model,
                messages,
                max_tokens: None,
                max_completion_tokens: input.max_output_tokens,
                temperature: input.temperature,
                n: None,
                stream: input.stream,
                stream_options: None,
            },
            limits,
        )
    }

    fn validate(input: ChatInput, limits: &Limits) -> Result<Self> {
        if !valid_alias(&input.model)
            || input.messages.is_empty()
            || input.messages.len() > limits.maximum_messages
            || input
                .messages
                .iter()
                .any(|message| message.content.is_empty())
            || input.n.is_some_and(|count| count != 1)
            || (input.max_tokens.is_some() && input.max_completion_tokens.is_some())
            || (!input.stream && input.stream_options.is_some())
        {
            return Err("invalid_request_error");
        }
        let maximum_tokens = input
            .max_completion_tokens
            .or(input.max_tokens)
            .unwrap_or(limits.default_output_tokens);
        let temperature = input.temperature.unwrap_or(limits.default_temperature);
        if maximum_tokens == 0
            || maximum_tokens > limits.maximum_output_tokens
            || !temperature.is_finite()
            || !(0.0..=2.0).contains(&temperature)
        {
            return Err("invalid_request_error");
        }
        Ok(Self {
            alias: input.model,
            messages: input.messages,
            maximum_tokens,
            temperature,
            stream: input.stream,
            include_usage: input.stream_options.unwrap_or_default().include_usage,
        })
    }

    pub fn tokenize_payload(&self, backend_model: &str) -> Value {
        json!({"model":backend_model,"messages":self.messages,"add_generation_prompt":true,"continue_final_message":false,"add_special_tokens":false,"chat_template_kwargs":{"enable_thinking":false},"return_token_strs":false})
    }

    pub fn completion_payload(&self, backend_model: &str) -> Value {
        let mut value = json!({"model":backend_model,"messages":self.messages,"max_tokens":self.maximum_tokens,"temperature":self.temperature,"n":1,"stream":self.stream,"chat_template_kwargs":{"enable_thinking":false}});
        if self.stream {
            // Always collect actual final usage; client-facing inclusion is independent.
            value["stream_options"] = json!({"include_usage":true});
        }
        value
    }

    pub fn validate_tokenization(&self, bytes: &[u8], limits: &Limits) -> Result<u32> {
        #[derive(Deserialize)]
        struct Tokenization {
            count: u32,
            max_model_len: u32,
            tokens: Vec<u32>,
        }
        if bytes.len() as u64 > limits.response_bytes {
            return Err("upstream_response_invalid");
        }
        let value: Tokenization =
            serde_json::from_slice(bytes).map_err(|_| "upstream_response_invalid")?;
        if value.count == 0
            || value.count as usize != value.tokens.len()
            || value.max_model_len == 0
        {
            return Err("upstream_response_invalid");
        }
        let ceiling = value.max_model_len.min(limits.maximum_context_tokens);
        if value.count > ceiling || self.maximum_tokens > ceiling - value.count {
            return Err("context_length_exceeded");
        }
        Ok(value.count)
    }
}

fn parse_input<'a, T: Deserialize<'a>>(bytes: &'a [u8]) -> Result<T> {
    serde_json::from_slice(bytes).map_err(|error| {
        if error.to_string().starts_with("unknown field") {
            "unsupported_parameter"
        } else {
            "invalid_request_error"
        }
    })
}

#[derive(Clone, Deserialize, Serialize, Debug, PartialEq, Eq)]
pub struct Usage {
    pub prompt_tokens: u32,
    pub completion_tokens: u32,
    pub total_tokens: u32,
}

impl Usage {
    pub fn validate(&self, prompt: u32, output: u32) -> Result<()> {
        if self.prompt_tokens != prompt
            || self.completion_tokens > output
            || self.prompt_tokens.checked_add(self.completion_tokens) != Some(self.total_tokens)
        {
            return Err("upstream_response_invalid");
        }
        Ok(())
    }
}

#[derive(Clone, Copy, Deserialize, Serialize)]
#[serde(rename_all = "snake_case")]
pub(crate) enum FinishReason {
    Stop,
    Length,
    ContentFilter,
}

pub(crate) struct Completion {
    id: String,
    created: u64,
    pub(crate) text: String,
    pub(crate) reason: FinishReason,
    pub(crate) usage: Usage,
}

impl Completion {
    pub(crate) fn chat(&self, alias: &str) -> Value {
        json!({"id":self.id,"object":"chat.completion","created":self.created,"model":alias,"choices":[{"index":0,"message":{"role":"assistant","content":self.text},"finish_reason":self.reason}],"usage":self.usage})
    }
}

pub(crate) fn normalize_completion(
    bytes: &[u8],
    request: &ChatRequest,
    backend_model: &str,
    prompt_tokens: u32,
    limits: &Limits,
) -> Result<Completion> {
    if bytes.len() as u64 > limits.response_bytes {
        return Err("upstream_response_invalid");
    }
    #[derive(Deserialize)]
    struct AssistantMessage {
        role: String,
        content: String,
        tool_calls: Option<Value>,
        function_call: Option<Value>,
    }
    #[derive(Deserialize)]
    struct Choice {
        index: u32,
        message: AssistantMessage,
        finish_reason: FinishReason,
    }
    #[derive(Deserialize)]
    struct BackendCompletion {
        id: String,
        object: String,
        created: u64,
        model: String,
        choices: Vec<Choice>,
        usage: Usage,
    }
    let value: BackendCompletion =
        serde_json::from_slice(bytes).map_err(|_| "upstream_response_invalid")?;
    let valid_id = !value.id.is_empty()
        && value.id.len() <= 128
        && value
            .id
            .bytes()
            .all(|value| value.is_ascii_alphanumeric() || b"-_.".contains(&value));
    if !valid_id
        || value.object != "chat.completion"
        || value.model != backend_model
        || value.choices.len() != 1
    {
        return Err("upstream_response_invalid");
    }
    let choice = value
        .choices
        .into_iter()
        .next()
        .ok_or("upstream_response_invalid")?;
    if choice.index != 0
        || choice.message.role != "assistant"
        || choice.message.tool_calls.is_some()
        || choice.message.function_call.is_some()
    {
        return Err("upstream_response_invalid");
    }
    value
        .usage
        .validate(prompt_tokens, request.maximum_tokens)?;
    Ok(Completion {
        id: value.id,
        created: value.created,
        text: choice.message.content,
        reason: choice.finish_reason,
        usage: value.usage,
    })
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn responses_input_is_translated_once_for_tokenization_and_generation() {
        let limits = Limits::default();
        let request = ChatRequest::parse_responses(
            br#"{"model":"public-alias","input":"Synthetic prompt","instructions":"Synthetic instructions","max_output_tokens":16,"temperature":0.5,"background":false,"store":false,"stream":true}"#,
            &limits,
        ).unwrap();
        let tokenize = request.tokenize_payload("internal");
        let generation = request.completion_payload("internal");
        assert_eq!(
            tokenize["messages"],
            json!([
                {"role":"system","content":"Synthetic instructions"},
                {"role":"user","content":"Synthetic prompt"}
            ])
        );
        assert_eq!(tokenize["messages"], generation["messages"]);
        assert_eq!(generation["max_tokens"], 16);
        assert_eq!(generation["temperature"], 0.5);
        assert_eq!(generation["stream_options"]["include_usage"], true);
        assert!(
            ChatRequest::parse_responses(
                br#"{"model":"public-alias","input":"first","input":"second"}"#,
                &limits
            )
            .is_err()
        );
    }

    #[test]
    fn configured_defaults_apply_without_rebuilding_request_logic() {
        let limits = Limits {
            default_output_tokens: 16,
            default_temperature: 0.5,
            ..Limits::default()
        };
        limits.validate().unwrap();
        let request = ChatRequest::parse(
            br#"{"model":"public-alias","messages":[{"role":"user","content":"Synthetic input"}]}"#,
            &limits,
        )
        .unwrap();
        assert_eq!(request.maximum_tokens, 16);
        assert_eq!(request.temperature, 0.5);
    }

    fn input(extra: Value) -> Vec<u8> {
        let mut value = json!({"model":"sample-model","messages":[{"role":"user","content":"Synthetic multilingual content \u{4e2d}\u{6587}"}],"max_tokens":16});
        for (key, item) in extra.as_object().unwrap() {
            value[key] = item.clone();
        }
        serde_json::to_vec(&value).unwrap()
    }

    #[test]
    fn strict_chat_subset_rejects_duplicate_unsupported_and_conflicting_values() {
        let limits = Limits::default();
        for extra in [
            json!({"tools":[]}),
            json!({"model":"http://127.0.0.1"}),
            json!({"n":2}),
            json!({"max_completion_tokens":4}),
            json!({"stream_options":{"include_usage":true}}),
            json!({"messages":[{"role":"user","content":[{"type":"text","text":"unsupported"}]}]}),
        ] {
            assert!(ChatRequest::parse(&input(extra), &limits).is_err());
        }
        assert!(
            ChatRequest::parse(br#"{"model":"a","model":"b","messages":[]}"#, &limits).is_err()
        );
        assert!(ChatRequest::parse(b"\xff", &limits).is_err());
        let request = ChatRequest::parse(&input(json!({"stream":true})), &limits).unwrap();
        assert!(!request.include_usage);
        assert_eq!(
            request.completion_payload("approved-internal")["stream_options"]["include_usage"],
            true
        );
        assert_eq!(
            request.tokenize_payload("approved-internal")["chat_template_kwargs"],
            request.completion_payload("approved-internal")["chat_template_kwargs"]
        );
    }

    #[test]
    fn synthetic_tokenizer_response_controls_context_without_character_estimates() {
        let limits = Limits {
            maximum_context_tokens: 32,
            maximum_output_tokens: 16,
            ..Limits::default()
        };
        let request = ChatRequest::parse(&input(json!({})), &limits).unwrap();
        let response = |count: u32, maximum: u32| {
            serde_json::to_vec(
                &json!({"count":count,"max_model_len":maximum,"tokens":vec![1u32;count as usize]}),
            )
            .unwrap()
        };
        assert_eq!(
            request.validate_tokenization(&response(16, 2048), &limits),
            Ok(16)
        );
        assert_eq!(
            request.validate_tokenization(&response(17, 2048), &limits),
            Err("context_length_exceeded")
        );
        assert_eq!(
            request.validate_tokenization(&response(16, 20), &limits),
            Err("context_length_exceeded")
        );
        assert_eq!(
            request.validate_tokenization(
                br#"{"count":16,"max_model_len":2048,"tokens":[1]}"#,
                &limits
            ),
            Err("upstream_response_invalid")
        );
    }

    #[test]
    fn completion_identity_usage_and_supported_surface_are_verified() {
        let limits = Limits::default();
        let request = ChatRequest::parse(&input(json!({})), &limits).unwrap();
        let mut value = json!({"id":"chatcmpl-synthetic","object":"chat.completion","created":1,"model":"approved-internal","choices":[{"index":0,"message":{"role":"assistant","content":"synthetic response"},"finish_reason":"stop"}],"usage":{"prompt_tokens":7,"completion_tokens":2,"total_tokens":9},"private_diagnostic":"must never be forwarded"});
        let completion = normalize_completion(
            &serde_json::to_vec(&value).unwrap(),
            &request,
            "approved-internal",
            7,
            &limits,
        )
        .unwrap();
        let normalized = completion.chat(&request.alias);
        assert_eq!(normalized["model"], "sample-model");
        assert!(normalized.get("private_diagnostic").is_none());
        assert_eq!(completion.usage.total_tokens, 9);
        value["usage"]["total_tokens"] = json!(10);
        assert!(
            normalize_completion(
                &serde_json::to_vec(&value).unwrap(),
                &request,
                "approved-internal",
                7,
                &limits
            )
            .is_err()
        );
        value["usage"]["total_tokens"] = json!(9);
        value["model"] = json!("wrong-model");
        assert!(
            normalize_completion(
                &serde_json::to_vec(&value).unwrap(),
                &request,
                "approved-internal",
                7,
                &limits
            )
            .is_err()
        );
    }
}
