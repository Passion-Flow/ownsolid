use super::*;

fn lookup_schema() -> Value {
    json!({
        "type":"object", "properties":{"key":{"type":"string"}},
        "required":["key"], "additionalProperties":false
    })
}

#[tokio::test]
async fn unsupported_named_tools_and_result_histories_stop_before_admission() {
    let fixture = Fixture::new().await;
    let responses = [
        json!({
            "model":"public-alias", "input":"Look up a synthetic record.", "store":false,
            "tools":[{"type":"function","name":"lookup","parameters":lookup_schema(),"strict":true}],
            "tool_choice":{"type":"function","name":"lookup"}, "parallel_tool_calls":false
        }),
        json!({
            "model":"public-alias", "store":false, "input":[
                {"type":"message","role":"user","content":"Look up a synthetic record."},
                {"type":"function_call","id":"fc_synthetic","call_id":"call_synthetic",
                    "name":"lookup","arguments":"{\"key\":\"synthetic-record\"}"},
                {"type":"function_call_output","call_id":"call_synthetic","output":"synthetic result"}
            ]
        }),
    ];
    let messages = [
        json!({
            "model":"public-alias","max_tokens":16,
            "messages":[{"role":"user","content":"Look up a synthetic record."}],
            "tools":[{"name":"lookup","input_schema":lookup_schema(),"strict":true}],
            "tool_choice":{"type":"tool","name":"lookup"}
        }),
        json!({
            "model":"public-alias","max_tokens":16,"messages":[
                {"role":"user","content":"Look up a synthetic record."},
                {"role":"assistant","content":[{"type":"tool_use","id":"toolu_synthetic",
                    "name":"lookup","input":{"key":"synthetic-record"}}]},
                {"role":"user","content":[{"type":"tool_result","tool_use_id":"toolu_synthetic",
                    "content":"synthetic result"}]}
            ]
        }),
    ];
    for (send, bodies) in [
        (
            responses_request as fn(&Fixture, bool) -> reqwest::RequestBuilder,
            responses,
        ),
        (messages_request, messages),
    ] {
        for body in bodies {
            let response = send(&fixture, false).json(&body).send().await.unwrap();
            assert_eq!(response.status(), StatusCode::BAD_REQUEST);
            let body: Value = response.json().await.unwrap();
            let code = body["error"]["code"].as_str().unwrap();
            assert!(matches!(
                code,
                "unsupported_parameter" | "invalid_request_error"
            ));
            assert!(!body.to_string().contains("synthetic-record"));
        }
    }
    assert_eq!(fixture.state.model_checks.load(Ordering::SeqCst), 0);
    assert!(fixture.state.admissions.lock().unwrap().is_empty());
    assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 0);
    assert!(fixture.app().spool.next_pending().unwrap().is_none());
}

#[tokio::test]
async fn unsolicited_tool_delta_and_disconnect_never_emit_usage_or_redispatch() {
    for (send, delta, terminal) in [
        (
            responses_request as fn(&Fixture, bool) -> reqwest::RequestBuilder,
            "response.output_text.delta",
            "response.failed",
        ),
        (messages_request, "content_block_delta", "error"),
    ] {
        for disconnect in [false, true] {
            let fixture = Fixture::with_quota(Some(json!({"tokens":{"daily":32}}))).await;
            fixture
                .state
                .backend_mode
                .store(UNSOLICITED_TOOL_DELTA, Ordering::SeqCst);
            let mut response = send(&fixture, true).send().await.unwrap();
            assert_eq!(response.status(), StatusCode::OK);
            let id = response.headers()["x-request-id"]
                .to_str()
                .unwrap()
                .to_owned();
            let prefix = read_named_delta(&mut response, delta).await;
            assert!(fixture.app().spool.next_pending().unwrap().is_none());
            if disconnect {
                drop(response);
            } else {
                fixture.state.complete_stream.notify_one();
                let tail = response.bytes().await.unwrap();
                let mut all = prefix;
                all.extend(tail);
                let events = named_events(&all);
                assert_eq!(events.last().unwrap()["type"], terminal);
                let wire = String::from_utf8(all).unwrap();
                assert!(wire.contains("upstream_response_invalid"));
                for forbidden in [
                    "call_unexpected",
                    "private_synthetic_tool",
                    "private-synthetic-arguments",
                    "response.completed",
                    "message_stop",
                    "\"total_tokens\"",
                    "\"output_tokens\":2",
                ] {
                    assert!(!wire.contains(forbidden), "{forbidden}");
                }
            }
            wait_until(|| {
                fixture.state.dropped_generations.load(Ordering::SeqCst) == 1
                    && fixture.app().spool.next_pending().unwrap().is_some()
            })
            .await;
            let receipt = fixture.app().spool.next_pending().unwrap().unwrap();
            assert_eq!(receipt.request_id.to_string(), id);
            assert_eq!(
                receipt.status,
                if disconnect {
                    Status::ClientCancelled
                } else {
                    Status::InvalidResponse
                }
            );
            assert!(receipt.output_started);
            assert!(receipt.usage.is_none());

            // Another generation needs 23 tokens. The uncertain first call retains its 23 of 32.
            let denied = fixture.chat(false).send().await.unwrap();
            assert_eq!(denied.status(), StatusCode::TOO_MANY_REQUESTS);
            assert_eq!(
                denied.json::<Value>().await.unwrap()["error"]["code"],
                "daily_token_budget_exceeded"
            );
            assert_eq!(fixture.state.generations.load(Ordering::SeqCst), 1);
        }
    }
}
