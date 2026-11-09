use crate::configuration::Result;
use crate::protocol::{ChatRequest, Completion, FinishReason, Usage};
use crate::streaming::named_event;
use serde_json::{Value, json};
use uuid::Uuid;

/// Metadata shared by JSON responses and all lifecycle events of one request.
pub(crate) struct ResponseIdentity {
    id: String,
    item_id: String,
    alias: String,
    created_at: i64,
    maximum_tokens: u32,
    temperature: f64,
}

impl ResponseIdentity {
    pub(crate) fn new(id: Uuid, request: &ChatRequest) -> Self {
        Self {
            id: format!("resp_{id}"),
            item_id: format!("msg_{id}"),
            alias: request.alias.clone(),
            created_at: time::OffsetDateTime::now_utc().unix_timestamp(),
            maximum_tokens: request.maximum_tokens,
            temperature: request.temperature,
        }
    }

    fn response(
        &self,
        status: &str,
        output: Value,
        usage: Value,
        incomplete_details: Value,
        error: Value,
    ) -> Value {
        json!({
            "id": self.id,
            "object": "response",
            "created_at": self.created_at,
            "model": self.alias,
            "status": status,
            "output": output,
            "usage": usage,
            "incomplete_details": incomplete_details,
            "error": error,
            "max_output_tokens": self.maximum_tokens,
            "temperature": self.temperature,
            "background": false,
            "store": false,
            "text": {"format": {"type": "text"}},
            "tools": [],
            "tool_choice": "none",
            "parallel_tool_calls": false,
            "truncation": "disabled"
        })
    }

    fn item(&self, status: &str, content: Value) -> Value {
        json!({
            "id": self.item_id,
            "type": "message",
            "role": "assistant",
            "status": status,
            "content": content
        })
    }

    fn result(&self, text: &str, reason: FinishReason, usage: &Usage) -> Value {
        let (status, incomplete_reason) = status(reason);
        self.response(
            status,
            json!([self.item(status, json!([text_part(text)]))]),
            json!({
                "input_tokens": usage.prompt_tokens,
                "output_tokens": usage.completion_tokens,
                "total_tokens": usage.total_tokens
            }),
            incomplete_reason.map_or(Value::Null, |reason| json!({"reason": reason})),
            Value::Null,
        )
    }

    pub(crate) fn complete(&self, completion: &Completion) -> Value {
        self.result(&completion.text, completion.reason, &completion.usage)
    }
}

fn status(reason: FinishReason) -> (&'static str, Option<&'static str>) {
    match reason {
        FinishReason::Stop => ("completed", None),
        FinishReason::Length => ("incomplete", Some("max_output_tokens")),
        FinishReason::ContentFilter => ("incomplete", Some("content_filter")),
    }
}

fn text_part(text: &str) -> Value {
    json!({"type": "output_text", "text": text, "annotations": [], "logprobs": []})
}

/// Projects validated upstream text into one foreground Responses stream.
pub(crate) struct ResponsesStream {
    identity: ResponseIdentity,
    maximum_text_bytes: usize,
    text: String,
    sequence: u64,
}

impl ResponsesStream {
    pub(crate) fn new(identity: ResponseIdentity, maximum_text_bytes: usize) -> Self {
        Self {
            identity,
            maximum_text_bytes,
            text: String::new(),
            sequence: 0,
        }
    }

    pub(crate) fn start(&mut self) -> Vec<u8> {
        let mut output = Vec::new();
        let response = self.identity.response(
            "in_progress",
            json!([]),
            Value::Null,
            Value::Null,
            Value::Null,
        );
        event(
            &mut output,
            &mut self.sequence,
            "response.created",
            json!({"response": response}),
        );
        event(
            &mut output,
            &mut self.sequence,
            "response.in_progress",
            json!({"response": response}),
        );
        output
    }

    pub(crate) fn delta(&mut self, text: &str) -> Result<Option<Vec<u8>>> {
        if text.is_empty() {
            return Ok(None);
        }
        if text.len() > self.maximum_text_bytes - self.text.len() {
            return Err("upstream_response_invalid");
        }
        let mut output = Vec::new();
        let mut sequence = self.sequence;
        if self.text.is_empty() {
            self.start_item(&mut output, &mut sequence);
        }
        self.text.push_str(text);
        event(
            &mut output,
            &mut sequence,
            "response.output_text.delta",
            json!({
                "item_id": self.identity.item_id, "output_index": 0, "content_index": 0,
                "delta": text, "logprobs": []
            }),
        );
        self.sequence = sequence;
        Ok(Some(output))
    }

    fn start_item(&self, output: &mut Vec<u8>, sequence: &mut u64) {
        event(
            output,
            sequence,
            "response.output_item.added",
            json!({
                "output_index": 0, "item": self.identity.item("in_progress", json!([]))
            }),
        );
        event(
            output,
            sequence,
            "response.content_part.added",
            json!({
                "item_id": self.identity.item_id, "output_index": 0, "content_index": 0,
                "part": text_part("")
            }),
        );
    }

    pub(crate) fn finish(&self, reason: FinishReason, usage: &Usage) -> Vec<u8> {
        let mut output = Vec::new();
        let mut sequence = self.sequence;
        if self.text.is_empty() {
            self.start_item(&mut output, &mut sequence);
        }
        let (status, _) = status(reason);
        let part = text_part(&self.text);
        event(
            &mut output,
            &mut sequence,
            "response.output_text.done",
            json!({
                "item_id": self.identity.item_id, "output_index": 0, "content_index": 0,
                "text": self.text, "logprobs": []
            }),
        );
        event(
            &mut output,
            &mut sequence,
            "response.content_part.done",
            json!({
                "item_id": self.identity.item_id, "output_index": 0, "content_index": 0, "part": part
            }),
        );
        event(
            &mut output,
            &mut sequence,
            "response.output_item.done",
            json!({
                "output_index": 0, "item": self.identity.item(status, json!([part]))
            }),
        );
        let name = match reason {
            FinishReason::Stop => "response.completed",
            FinishReason::Length | FinishReason::ContentFilter => "response.incomplete",
        };
        event(
            &mut output,
            &mut sequence,
            name,
            json!({
                "response": self.identity.result(&self.text, reason, usage)
            }),
        );
        output
    }

    pub(crate) fn failure(&self, code: &'static str) -> Vec<u8> {
        let mut output = Vec::new();
        let mut sequence = self.sequence;
        event(
            &mut output,
            &mut sequence,
            "response.failed",
            json!({
                "response": self.identity.response(
                    "failed", json!([]), Value::Null, Value::Null,
                    json!({"code": code, "message": "The request could not be completed."}),
                )
            }),
        );
        output
    }
}

fn event(output: &mut Vec<u8>, sequence: &mut u64, name: &str, mut data: Value) {
    data["type"] = json!(name);
    data["sequence_number"] = json!(*sequence);
    *sequence += 1;
    named_event(output, name, data);
}
