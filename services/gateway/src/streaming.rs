use crate::configuration::{Limits, Result};
use crate::protocol::{ChatRequest, Usage};
use serde::Deserialize;
use serde_json::{Value, json};

#[derive(Deserialize)]
struct Delta {
    role: Option<String>,
    content: Option<String>,
    tool_calls: Option<Value>,
    function_call: Option<Value>,
}

#[derive(Deserialize)]
struct Choice {
    index: u32,
    delta: Delta,
    finish_reason: Option<String>,
}

#[derive(Deserialize)]
struct Chunk {
    id: String,
    object: String,
    created: u64,
    model: String,
    choices: Vec<Choice>,
    usage: Option<Usage>,
}

/// Retains one bounded event; the transport consumes each returned event before reading more.
pub struct Decoder {
    buffer: Vec<u8>,
    line_start: usize,
    previous_cr: bool,
    completed_cr_bytes: Option<usize>,
    total_bytes: u64,
    maximum_event_bytes: u64,
    maximum_total_bytes: u64,
    backend_model: String,
    alias: String,
    include_usage: bool,
    prompt_tokens: u32,
    maximum_tokens: u32,
    identity: Option<(String, u64)>,
    finish_seen: bool,
    done_seen: bool,
    usage: Option<Usage>,
}

impl Decoder {
    pub fn new(
        request: &ChatRequest,
        backend_model: &str,
        prompt_tokens: u32,
        limits: &Limits,
    ) -> Self {
        Self {
            buffer: Vec::new(),
            line_start: 0,
            previous_cr: false,
            completed_cr_bytes: None,
            total_bytes: 0,
            maximum_event_bytes: limits.stream_event_bytes,
            maximum_total_bytes: limits.stream_total_bytes,
            backend_model: backend_model.to_owned(),
            alias: request.alias.clone(),
            include_usage: request.include_usage,
            prompt_tokens,
            maximum_tokens: request.maximum_tokens,
            identity: None,
            finish_seen: false,
            done_seen: false,
            usage: None,
        }
    }

    /// Consumes at most one event, allowing the caller to apply client backpressure immediately.
    pub fn consume(&mut self, bytes: &[u8]) -> Result<(usize, Option<Vec<u8>>)> {
        for (index, byte) in bytes.iter().copied().enumerate() {
            self.total_bytes += 1;
            if self.total_bytes > self.maximum_total_bytes {
                return Err("upstream_response_invalid");
            }
            if self.previous_cr {
                self.previous_cr = false;
                let completed = self.completed_cr_bytes.take();
                if byte == b'\n' {
                    // A fragmented CRLF is one line ending. If its CR dispatched an
                    // event, still charge the LF to that event's original byte limit.
                    let event_bytes = if let Some(size) = completed {
                        size + 1
                    } else {
                        self.buffer.push(byte);
                        self.line_start = self.buffer.len();
                        self.buffer.len()
                    };
                    if event_bytes as u64 > self.maximum_event_bytes {
                        return Err("upstream_response_invalid");
                    }
                    continue;
                }
            }
            if self.done_seen {
                if !byte.is_ascii_whitespace() {
                    return Err("upstream_response_invalid");
                }
                continue;
            }
            self.buffer.push(byte);
            if self.buffer.len() as u64 > self.maximum_event_bytes {
                return Err("upstream_response_invalid");
            }
            if matches!(byte, b'\r' | b'\n') {
                let blank = self.buffer.len() == self.line_start + 1;
                self.line_start = self.buffer.len();
                self.previous_cr = byte == b'\r';
                if blank {
                    if self.previous_cr {
                        self.completed_cr_bytes = Some(self.buffer.len());
                    }
                    let event = std::mem::take(&mut self.buffer);
                    self.line_start = 0;
                    return Ok((index + 1, self.event(&event)?));
                }
            }
        }
        Ok((bytes.len(), None))
    }

    fn event(&mut self, bytes: &[u8]) -> Result<Option<Vec<u8>>> {
        let text = std::str::from_utf8(bytes).map_err(|_| "upstream_response_invalid")?;
        let mut data = String::new();
        let mut data_seen = false;
        for line in text.split(['\r', '\n']) {
            if line.is_empty() || line.starts_with(':') {
                continue;
            }
            let value = line
                .strip_prefix("data:")
                .ok_or("upstream_response_invalid")?;
            if data_seen {
                data.push('\n');
            }
            data_seen = true;
            data.push_str(value.strip_prefix(' ').unwrap_or(value));
        }
        if data.is_empty() {
            return Ok(None);
        }
        if data == "[DONE]" {
            if !self.finish_seen || self.usage.is_none() {
                return Err("upstream_response_invalid");
            }
            // The transport persists its outcome before emitting DONE after the actual upstream EOF.
            self.done_seen = true;
            return Ok(None);
        }
        let value: Chunk = serde_json::from_str(&data).map_err(|_| "upstream_response_invalid")?;
        if value.object != "chat.completion.chunk"
            || value.model != self.backend_model
            || value.id.is_empty()
            || value.id.len() > 128
            || !value
                .id
                .bytes()
                .all(|value| value.is_ascii_alphanumeric() || b"-_.".contains(&value))
        {
            return Err("upstream_response_invalid");
        }
        if let Some((id, created)) = &self.identity {
            if id != &value.id || *created != value.created {
                return Err("upstream_response_invalid");
            }
        } else {
            self.identity = Some((value.id.clone(), value.created));
        }
        let output = if value.choices.is_empty() {
            if !self.finish_seen || self.usage.is_some() {
                return Err("upstream_response_invalid");
            }
            let usage = value.usage.ok_or("upstream_response_invalid")?;
            usage.validate(self.prompt_tokens, self.maximum_tokens)?;
            self.usage = Some(usage.clone());
            if !self.include_usage {
                return Ok(None);
            }
            json!({"id":value.id,"object":"chat.completion.chunk","created":value.created,"model":self.alias,"choices":[],"usage":usage})
        } else {
            if self.finish_seen || value.choices.len() != 1 || value.usage.is_some() {
                return Err("upstream_response_invalid");
            }
            let choice = &value.choices[0];
            if choice.index != 0
                || choice
                    .delta
                    .role
                    .as_ref()
                    .is_some_and(|role| role != "assistant")
                || choice.delta.tool_calls.is_some()
                || choice.delta.function_call.is_some()
                || choice.finish_reason.as_ref().is_some_and(|reason| {
                    !matches!(reason.as_str(), "stop" | "length" | "content_filter")
                })
            {
                return Err("upstream_response_invalid");
            }
            self.finish_seen = choice.finish_reason.is_some();
            let mut delta = serde_json::Map::new();
            if let Some(role) = &choice.delta.role {
                delta.insert("role".into(), json!(role));
            }
            if let Some(content) = &choice.delta.content {
                delta.insert("content".into(), json!(content));
            }
            json!({"id":value.id,"object":"chat.completion.chunk","created":value.created,"model":self.alias,"choices":[{"index":0,"delta":delta,"finish_reason":choice.finish_reason}],"usage":null})
        };
        let json = serde_json::to_vec(&output).map_err(|_| "upstream_response_invalid")?;
        let mut encoded = Vec::with_capacity(json.len() + b"data: \n\n".len());
        encoded.extend_from_slice(b"data: ");
        encoded.extend(json);
        encoded.extend_from_slice(b"\n\n");
        Ok(Some(encoded))
    }

    pub fn finish(self) -> Result<Usage> {
        if !self.done_seen || !self.buffer.iter().all(u8::is_ascii_whitespace) {
            return Err("upstream_response_invalid");
        }
        self.usage.ok_or("upstream_response_invalid")
    }
}

pub const DONE_EVENT: &[u8] = b"data: [DONE]\n\n";

#[cfg(test)]
mod tests {
    use super::*;

    fn request(include_usage: bool) -> ChatRequest {
        ChatRequest::parse(&serde_json::to_vec(&json!({"model":"public-alias","messages":[{"role":"user","content":"synthetic"}],"stream":true,"stream_options":{"include_usage":include_usage},"max_tokens":16})).unwrap(),&Limits::default()).unwrap()
    }
    fn event(choices: Value, usage: Value) -> Vec<u8> {
        format!("data: {}\n\n",json!({"id":"chatcmpl-fixture","object":"chat.completion.chunk","created":1,"model":"internal-model","choices":choices,"usage":usage,"private_diagnostic":"must never forward"})).into_bytes()
    }
    fn stream() -> Vec<u8> {
        let mut bytes = event(
            json!([{"index":0,"delta":{"role":"assistant","content":"\u{4e2d}\u{6587}"},"finish_reason":null}]),
            Value::Null,
        );
        bytes.extend(event(
            json!([{"index":0,"delta":{},"finish_reason":"stop"}]),
            Value::Null,
        ));
        bytes.extend(event(
            json!([]),
            json!({"prompt_tokens":7,"completion_tokens":2,"total_tokens":9}),
        ));
        bytes.extend(DONE_EVENT);
        bytes
    }
    fn line_endings(bytes: &[u8], endings: &[&str]) -> Vec<u8> {
        let mut result = Vec::new();
        let mut line = 0;
        for byte in bytes {
            if *byte == b'\n' {
                result.extend_from_slice(endings[line % endings.len()].as_bytes());
                line += 1;
            } else {
                result.push(*byte);
            }
        }
        result
    }
    fn feed(decoder: &mut Decoder, bytes: &[u8]) -> Result<Vec<Vec<u8>>> {
        let mut offset = 0;
        let mut result = Vec::new();
        while offset < bytes.len() {
            let (used, event) = decoder.consume(&bytes[offset..])?;
            assert!(used > 0);
            offset += used;
            if let Some(value) = event {
                result.push(value)
            }
        }
        Ok(result)
    }
    #[test]
    fn fragmented_line_endings_preserve_alias_and_usage_without_early_done() {
        for include in [false, true] {
            for endings in [&["\n"][..], &["\r\n"], &["\r"], &["\r", "\r\n", "\n"]] {
                let request = request(include);
                let bytes = line_endings(&stream(), endings);
                for chunk_size in [1, 2, 7, bytes.len()] {
                    let mut decoder =
                        Decoder::new(&request, "internal-model", 7, &Limits::default());
                    let mut events = Vec::new();
                    for chunk in bytes.chunks(chunk_size) {
                        events.extend(feed(&mut decoder, chunk).unwrap())
                    }
                    assert_eq!(events.len(), if include { 3 } else { 2 });
                    for event in events {
                        let text = String::from_utf8(event).unwrap();
                        assert!(text.contains("public-alias"));
                        assert!(text.contains("\u{4e2d}\u{6587}") || !text.contains("content"));
                        assert!(!text.contains("internal-model"));
                        assert!(!text.contains("private_diagnostic"));
                        assert!(!text.contains("[DONE]"));
                    }
                    assert_eq!(decoder.finish().unwrap().total_tokens, 9);
                }
            }
        }
    }
    #[test]
    fn crlf_is_one_line_ending_and_unterminated_done_is_not_success() {
        let request = request(true);
        let bytes = stream();
        let prefix = &bytes[..bytes.len() - DONE_EVENT.len()];
        for ending in ["\r", "\n", "\r\n"] {
            let mut decoder = Decoder::new(&request, "internal-model", 7, &Limits::default());
            feed(&mut decoder, prefix).unwrap();
            assert!(
                feed(&mut decoder, format!("data: [DONE]{ending}").as_bytes())
                    .unwrap()
                    .is_empty()
            );
            assert!(decoder.finish().is_err());
        }
        let mut decoder = Decoder::new(&request, "internal-model", 7, &Limits::default());
        // The LF of a fragmented CRLF must not dispatch the partial JSON as an event.
        assert!(feed(&mut decoder, b"data: {\r").unwrap().is_empty());
        assert!(feed(&mut decoder, b"\n").unwrap().is_empty());
        assert!(decoder.finish().is_err());
    }
    #[test]
    fn empty_data_lines_cannot_turn_a_multiline_payload_into_done() {
        let request = request(false);
        let bytes = stream();
        let prefix = &bytes[..bytes.len() - DONE_EVENT.len()];
        for ending in ["\r", "\n", "\r\n"] {
            for payload in [
                b"data:\ndata: [DONE]\n\n".as_slice(),
                b"data: \n: heartbeat\ndata: [DONE]\n\n",
                b"data: [DONE]\ndata:\n\n",
            ] {
                let mut decoder = Decoder::new(&request, "internal-model", 7, &Limits::default());
                feed(&mut decoder, prefix).unwrap();
                assert!(feed(&mut decoder, &line_endings(payload, &[ending])).is_err());
            }
        }
    }
    #[test]
    fn comments_and_multiline_json_preserve_data_across_fragmented_crlf() {
        let request = request(true);
        let bytes = stream();
        let first_end = bytes.windows(2).position(|value| value == b"\n\n").unwrap() + 2;
        let first = String::from_utf8(bytes[..first_end].to_vec()).unwrap();
        let multiline = first.replacen("data: {", ": heartbeat\ndata:\ndata: {\ndata:", 1);
        let mut input = line_endings(multiline.as_bytes(), &["\r\n"]);
        input.extend_from_slice(&bytes[first_end..]);
        let mut expected = Decoder::new(&request, "internal-model", 7, &Limits::default());
        let expected_events = feed(&mut expected, &bytes).unwrap();
        let mut decoder = Decoder::new(&request, "internal-model", 7, &Limits::default());
        let mut events = Vec::new();
        for byte in input {
            events.extend(feed(&mut decoder, &[byte]).unwrap());
        }
        assert_eq!(events, expected_events);
        assert_eq!(decoder.finish().unwrap().total_tokens, 9);
    }
    #[test]
    fn raw_event_and_total_limits_include_crlf_even_after_event_dispatch() {
        let request = request(false);
        for ending in ["\r", "\n", "\r\n"] {
            let comment = line_endings(b": heartbeat\n\n", &[ending]);
            for chunk_size in [1, comment.len()] {
                for maximum in [comment.len() - 1, comment.len()] {
                    let limits = Limits {
                        stream_event_bytes: maximum as u64,
                        ..Limits::default()
                    };
                    let mut decoder = Decoder::new(&request, "internal-model", 7, &limits);
                    let result = comment
                        .chunks(chunk_size)
                        .try_for_each(|chunk| feed(&mut decoder, chunk).map(|_| ()));
                    assert_eq!(result.is_ok(), maximum == comment.len());
                }
            }
            let bytes = line_endings(&stream(), &[ending]);
            let limits = Limits {
                stream_total_bytes: bytes.len() as u64,
                ..Limits::default()
            };
            let mut decoder = Decoder::new(&request, "internal-model", 7, &limits);
            feed(&mut decoder, &bytes).unwrap();
            assert!(feed(&mut decoder, b" ").is_err());
        }
    }
    #[test]
    fn missing_usage_partial_event_changed_identity_and_duplicate_done_are_not_success() {
        let request = request(true);
        for bytes in [
            b"data: [DONE]\n\n".to_vec(),
            b"data: {\"partial\"".to_vec(),
            {
                let mut bytes = stream();
                bytes.extend(DONE_EVENT);
                bytes
            },
        ] {
            let mut decoder = Decoder::new(&request, "internal-model", 7, &Limits::default());
            if feed(&mut decoder, &bytes).is_ok() {
                assert!(decoder.finish().is_err())
            }
        }
        let mut decoder = Decoder::new(&request, "wrong-model", 7, &Limits::default());
        assert!(feed(&mut decoder, &stream()).is_err());
    }
    #[test]
    fn event_and_total_memory_bounds_apply_to_comments_and_unterminated_content() {
        let request = request(false);
        let limits = Limits {
            stream_event_bytes: 256,
            stream_total_bytes: 512,
            ..Limits::default()
        };
        let mut decoder = Decoder::new(&request, "internal-model", 7, &limits);
        assert!(feed(&mut decoder, &vec![b'x'; 257]).is_err());
        let mut decoder = Decoder::new(&request, "internal-model", 7, &limits);
        for _ in 0..64 {
            assert!(feed(&mut decoder, b":ping\n\n").is_ok())
        }
        assert!(feed(&mut decoder, &[b' '; 65]).is_err());
    }
}
