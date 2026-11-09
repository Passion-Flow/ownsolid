use crate::configuration::{Limits, Result};
use crate::protocol::{ChatRequest, Completion, FinishReason, Message, Role, parse_input};
use crate::streaming::named_event;
use axum::http::HeaderMap;
use serde::{Deserialize, Deserializer};
use serde_json::{Value, json};
use uuid::Uuid;

const VERSION: &str = "2026-10-21";

/// Select the supported Anthropic contract without accepting caller-provided authority.
pub(crate) fn validate_headers(headers: &HeaderMap) -> Result<()> {
    if headers.get_all("anthropic-version").iter().count() != 1
        || headers
            .get("anthropic-version")
            .is_none_or(|value| value != VERSION)
    {
        return Err("unsupported_api_version");
    }
    if headers.contains_key("anthropic-beta") || headers.contains_key("anthropic-workspace-id") {
        return Err("unsupported_parameter");
    }
    Ok(())
}

#[derive(Deserialize)]
#[serde(tag = "type", rename_all = "lowercase", deny_unknown_fields)]
enum TextBlock {
    Text { text: String },
}

#[derive(Deserialize)]
#[serde(untagged)]
enum TextContent {
    String(String),
    Block([TextBlock; 1]),
}

impl TextContent {
    fn into_text(self) -> String {
        match self {
            Self::String(text) | Self::Block([TextBlock::Text { text }]) => text,
        }
    }
}

fn present_system<'de, D: Deserializer<'de>>(
    deserializer: D,
) -> std::result::Result<Option<TextContent>, D::Error> {
    TextContent::deserialize(deserializer).map(Some)
}

#[derive(Deserialize)]
#[serde(rename_all = "lowercase")]
enum TurnRole {
    User,
    Assistant,
}

#[derive(Deserialize)]
#[serde(deny_unknown_fields)]
struct Turn {
    role: TurnRole,
    content: TextContent,
}

/// Normalize the supported Messages input into the common generation request.
pub(crate) fn parse(bytes: &[u8], limits: &Limits) -> Result<ChatRequest> {
    #[derive(Deserialize)]
    #[serde(deny_unknown_fields)]
    struct Input {
        model: String,
        max_tokens: u32,
        messages: Vec<Turn>,
        #[serde(default, deserialize_with = "present_system")]
        system: Option<TextContent>,
        #[serde(default)]
        stream: bool,
    }
    let input: Input = parse_input(bytes, limits)?;
    let count = input.messages.len();
    if count.is_multiple_of(2)
        || count + usize::from(input.system.is_some()) > limits.maximum_messages
    {
        return Err("invalid_request_error");
    }
    let mut messages = Vec::with_capacity(count + usize::from(input.system.is_some()));
    if let Some(system) = input.system {
        messages.push(Message {
            role: Role::System,
            content: system.into_text(),
        });
    }
    for (index, turn) in input.messages.into_iter().enumerate() {
        let role = match (index.is_multiple_of(2), turn.role) {
            (true, TurnRole::User) => Role::User,
            (false, TurnRole::Assistant) => Role::Assistant,
            _ => return Err("invalid_request_error"),
        };
        messages.push(Message {
            role,
            content: turn.content.into_text(),
        });
    }
    ChatRequest::from_text(
        input.model,
        messages,
        Some(input.max_tokens),
        None,
        input.stream,
        limits,
    )
}

fn stop_reason(reason: FinishReason) -> Result<&'static str> {
    match reason {
        FinishReason::Stop => Ok("end_turn"),
        FinishReason::Length => Ok("max_tokens"),
        FinishReason::ContentFilter => Err("upstream_response_invalid"),
    }
}

fn message(
    id: Uuid,
    alias: &str,
    text: &str,
    reason: Option<&str>,
    input_tokens: u32,
    output_tokens: u32,
) -> Value {
    let content = if text.is_empty() {
        json!([])
    } else {
        json!([{"type":"text","text":text}])
    };
    json!({
        "id": format!("msg_{id}"),
        "type": "message",
        "role": "assistant",
        "model": alias,
        "content": content,
        "stop_reason": reason,
        "stop_sequence": null,
        "stop_details": null,
        "usage": {"input_tokens": input_tokens, "output_tokens": output_tokens}
    })
}

/// Render a validated non-streaming completion.
pub(crate) fn complete(id: Uuid, alias: &str, completion: &Completion) -> Result<Value> {
    Ok(message(
        id,
        alias,
        &completion.text,
        Some(stop_reason(completion.reason)?),
        completion.usage.prompt_tokens,
        completion.usage.completion_tokens,
    ))
}

/// Projects a single indexed text block without keeping a copy of streamed text.
pub(crate) struct MessagesStream {
    id: Uuid,
    alias: String,
    prompt_tokens: u32,
    block_started: bool,
}

impl MessagesStream {
    pub(crate) fn new(id: Uuid, alias: &str, prompt_tokens: u32) -> Self {
        Self {
            id,
            alias: alias.to_owned(),
            prompt_tokens,
            block_started: false,
        }
    }

    pub(crate) fn start(&self) -> Vec<u8> {
        let mut output = Vec::new();
        named_event(
            &mut output,
            "message_start",
            json!({
                "type":"message_start",
                "message":message(self.id, &self.alias, "", None, self.prompt_tokens, 0)
            }),
        );
        output
    }

    pub(crate) fn delta(
        &mut self,
        text: &str,
        reason: Option<FinishReason>,
    ) -> Result<Option<Vec<u8>>> {
        if let Some(reason) = reason {
            stop_reason(reason)?;
        }
        if text.is_empty() {
            return Ok(None);
        }
        let mut output = Vec::new();
        if !self.block_started {
            named_event(
                &mut output,
                "content_block_start",
                json!({
                    "type":"content_block_start", "index":0, "content_block":{"type":"text","text":""}
                }),
            );
            self.block_started = true;
        }
        named_event(
            &mut output,
            "content_block_delta",
            json!({
                "type":"content_block_delta", "index":0, "delta":{"type":"text_delta","text":text}
            }),
        );
        Ok(Some(output))
    }

    pub(crate) fn finish(&self, reason: FinishReason, output_tokens: u32) -> Result<Vec<u8>> {
        let reason = stop_reason(reason)?;
        let mut output = Vec::new();
        if self.block_started {
            named_event(
                &mut output,
                "content_block_stop",
                json!({"type":"content_block_stop","index":0}),
            );
        }
        named_event(
            &mut output,
            "message_delta",
            json!({
                "type":"message_delta",
                "delta":{"stop_reason":reason,"stop_sequence":null,"stop_details":null},
                "usage":{"output_tokens":output_tokens}
            }),
        );
        named_event(&mut output, "message_stop", json!({"type":"message_stop"}));
        Ok(output)
    }

    pub(crate) fn failure(&self, code: &'static str) -> Vec<u8> {
        let mut output = Vec::new();
        named_event(
            &mut output,
            "error",
            crate::errors::messages_document(&code.into(), self.id),
        );
        output
    }
}
