use crate::configuration::Result;
use std::sync::Mutex;
use std::time::{Duration, Instant};

#[derive(Default)]
enum Phase {
    #[default]
    Ready,
    Open(Instant),
    Probing,
}

#[derive(Default)]
struct State {
    phase: Phase,
    generation: u64,
}

pub(super) struct ConnectionCircuit {
    cooldown: Duration,
    state: Mutex<State>,
}

pub(super) enum Observation {
    Response,
    ConnectionFailure,
    OtherFailure,
}

impl ConnectionCircuit {
    pub(super) fn new(cooldown: Duration) -> Self {
        Self {
            cooldown,
            state: Mutex::new(State::default()),
        }
    }

    pub(super) fn begin(&self, now: Instant) -> Result<Attempt<'_>> {
        let mut state = self.state.lock().map_err(|_| "upstream_unavailable")?;
        let probe = match state.phase {
            Phase::Ready => false,
            Phase::Open(until) if now >= until => {
                state.phase = Phase::Probing;
                true
            }
            Phase::Open(_) | Phase::Probing => return Err("upstream_unavailable"),
        };
        Ok(Attempt {
            circuit: self,
            generation: state.generation,
            probe,
        })
    }

    fn update(&self, generation: u64, reachable: bool, now: Instant) {
        let Ok(mut state) = self.state.lock() else {
            return;
        };
        // An attempt that started before a trip cannot change recovery's decision.
        if state.generation != generation {
            return;
        }
        let event = if reachable {
            state.phase = Phase::Ready;
            "gateway_backend_connection_restored"
        } else {
            state.phase = Phase::Open(now + self.cooldown);
            state.generation = state.generation.wrapping_add(1);
            "gateway_backend_connection_opened"
        };
        drop(state);
        eprintln!(
            "{}",
            serde_json::json!({"event":event,"cooldown_seconds":self.cooldown.as_secs()})
        );
    }
}

pub(super) struct Attempt<'a> {
    circuit: &'a ConnectionCircuit,
    generation: u64,
    probe: bool,
}

impl Attempt<'_> {
    pub(super) fn observe(mut self, observation: Observation, now: Instant) {
        if self.probe || matches!(observation, Observation::ConnectionFailure) {
            self.circuit.update(
                self.generation,
                matches!(observation, Observation::Response),
                now,
            );
        }
        self.probe = false;
    }
}

impl Drop for Attempt<'_> {
    fn drop(&mut self) {
        if self.probe {
            self.circuit.update(self.generation, false, Instant::now());
        }
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn one_probe_controls_recovery_and_older_failures_are_ignored() {
        let cooldown = Duration::from_secs(2);
        let circuit = ConnectionCircuit::new(cooldown);
        let now = Instant::now();
        let stale = circuit.begin(now).unwrap();
        circuit
            .begin(now)
            .unwrap()
            .observe(Observation::ConnectionFailure, now);
        assert!(circuit.begin(now).is_err());
        let resumed = now + cooldown;
        let probe = circuit.begin(resumed).unwrap();
        assert!(circuit.begin(resumed).is_err());
        probe.observe(Observation::Response, resumed);
        stale.observe(Observation::ConnectionFailure, resumed);
        assert!(circuit.begin(resumed).is_ok());
    }

    #[test]
    fn a_failed_or_cancelled_probe_reopens_the_circuit() {
        let cooldown = Duration::from_secs(10);
        let circuit = ConnectionCircuit::new(cooldown);
        let now = Instant::now();
        circuit
            .begin(now)
            .unwrap()
            .observe(Observation::ConnectionFailure, now);
        let resumed = now + cooldown;
        circuit
            .begin(resumed)
            .unwrap()
            .observe(Observation::OtherFailure, resumed);
        assert!(circuit.begin(resumed).is_err());
        let probe = circuit.begin(resumed + cooldown).unwrap();
        drop(probe);
        let after_cancel = Instant::now();
        assert!(circuit.begin(after_cancel).is_err());
        assert!(circuit.begin(after_cancel + cooldown).is_ok());
    }
}
