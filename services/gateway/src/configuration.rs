use nix::fcntl::OFlag;
use serde::{Deserialize, Serialize};
use std::fs::{self, OpenOptions};
use std::io::Read;
use std::net::SocketAddrV4;
use std::os::unix::fs::{MetadataExt, OpenOptionsExt};
use std::path::{Path, PathBuf};
use uuid::Uuid;

pub(crate) const MAX_REQUEST_SECONDS: u64 = 300;
pub(crate) const MAX_CONTEXT_TOKENS: u32 = 65_536;
const MAX_CONFIGURATION_BYTES: u64 = 65_536;
const MAX_SECRET_BYTES: u64 = 16_384;
const MAX_CERTIFICATE_BYTES: u64 = 1_048_576;
const MAX_BODY_BYTES: u64 = 1_048_576;
const MAX_MEMORY_BUDGET_BYTES: u64 = 268_435_456;
const MAX_SPOOL_BYTES: u64 = 268_435_456;
const MAX_OUTCOME_BYTES: u64 = 4_096;

#[derive(Clone, Deserialize, Serialize)]
#[serde(deny_unknown_fields)]
pub struct Configuration {
    pub gateway_id: Uuid,
    pub node_id: Uuid,
    pub listen_address: SocketAddrV4,
    pub backend_address: SocketAddrV4,
    pub control_origin: String,
    #[serde(default = "default_scope_id")]
    pub site_id: String,
    #[serde(default = "default_scope_id")]
    pub cell_id: String,
    pub control_ca_file: PathBuf,
    pub gateway_credential_file: PathBuf,
    pub tls_certificate_file: PathBuf,
    pub tls_private_key_file: PathBuf,
    pub state_directory: PathBuf,
    #[serde(default)]
    pub limits: Limits,
    #[serde(default, skip_serializing_if = "Option::is_none")]
    pub(crate) quota_policy: Option<crate::quota::Policy>,
}

#[derive(Clone, Debug, Deserialize, Serialize)]
#[serde(default, deny_unknown_fields)]
pub struct Limits {
    pub runtime_threads: usize,
    pub blocking_threads: usize,
    pub maximum_connections: u32,
    pub header_seconds: u64,
    pub header_bytes: usize,
    pub request_body_bytes: u64,
    pub request_memory_budget_bytes: u64,
    pub active_requests: u32,
    pub queued_requests: u32,
    pub queue_milliseconds: u64,
    pub connect_seconds: u64,
    pub control_seconds: u64,
    pub request_seconds: u64,
    pub idle_seconds: u64,
    pub shutdown_seconds: u64,
    pub operator_seconds: u64,
    pub response_bytes: u64,
    pub stream_event_bytes: u64,
    pub stream_total_bytes: u64,
    pub maximum_messages: usize,
    pub maximum_output_tokens: u32,
    pub default_output_tokens: u32,
    pub default_temperature: f64,
    pub maximum_context_tokens: u32,
    pub spool_max_entries: usize,
    pub spool_max_bytes: u64,
    pub outcome_retry_seconds: u64,
    pub control_idle_connections: usize,
    pub backend_idle_connections: usize,
    pub minimum_free_disk_bytes: u64,
}

impl Default for Limits {
    fn default() -> Self {
        Self {
            runtime_threads: 2,
            blocking_threads: 4,
            maximum_connections: 64,
            header_seconds: 5,
            header_bytes: 16_384,
            request_body_bytes: 65_536,
            request_memory_budget_bytes: 67_108_864,
            active_requests: 4,
            queued_requests: 8,
            queue_milliseconds: 2_000,
            connect_seconds: 2,
            control_seconds: 5,
            request_seconds: 90,
            idle_seconds: 15,
            shutdown_seconds: 5,
            operator_seconds: 30,
            response_bytes: 1_048_576,
            stream_event_bytes: 65_536,
            stream_total_bytes: 4_194_304,
            maximum_messages: 32,
            maximum_output_tokens: 512,
            default_output_tokens: 128,
            default_temperature: 0.0,
            maximum_context_tokens: 2048,
            spool_max_entries: 1_000,
            spool_max_bytes: 16_777_216,
            outcome_retry_seconds: 2,
            control_idle_connections: 2,
            backend_idle_connections: 4,
            minimum_free_disk_bytes: 67_108_864,
        }
    }
}

pub type Result<T> = std::result::Result<T, &'static str>;

impl Limits {
    pub fn validate(&self) -> Result<()> {
        if !(1..=8).contains(&self.runtime_threads)
            || !(1..=16).contains(&self.blocking_threads)
            || !(1..=1024).contains(&self.maximum_connections)
            || self.maximum_connections < self.active_requests + self.queued_requests
            || !(1..=30).contains(&self.header_seconds)
            || !(8192..=65_536).contains(&self.header_bytes)
            || !(1..=MAX_BODY_BYTES).contains(&self.request_body_bytes)
            || !(1..=64).contains(&self.active_requests)
            || self.queued_requests > 512
            || self.request_memory_budget_bytes > MAX_MEMORY_BUDGET_BYTES
            || u64::from(self.active_requests + self.queued_requests) * self.request_body_bytes
                > self.request_memory_budget_bytes
            || !(1..=10_000).contains(&self.queue_milliseconds)
            || !(1..=10).contains(&self.connect_seconds)
            || !(1..=30).contains(&self.control_seconds)
            || !(1..=MAX_REQUEST_SECONDS).contains(&self.request_seconds)
            || !(1..=60).contains(&self.idle_seconds)
            || self.idle_seconds > self.request_seconds
            || self.connect_seconds > self.control_seconds
            || !(1..=30).contains(&self.shutdown_seconds)
            || !(1..=300).contains(&self.operator_seconds)
            || !(1..=MAX_CERTIFICATE_BYTES).contains(&self.response_bytes)
            || !(256..=65_536).contains(&self.stream_event_bytes)
            || self.stream_event_bytes > self.stream_total_bytes
            || self.stream_total_bytes > 16_777_216
            || !(1..=128).contains(&self.maximum_messages)
            || !(1..=512).contains(&self.maximum_output_tokens)
            || !(1..=self.maximum_output_tokens).contains(&self.default_output_tokens)
            || !self.default_temperature.is_finite()
            || !(0.0..=2.0).contains(&self.default_temperature)
            || !(1..=MAX_CONTEXT_TOKENS).contains(&self.maximum_context_tokens)
            || self.maximum_output_tokens > self.maximum_context_tokens
            || !(1..=65_536).contains(&self.spool_max_entries)
            || self.spool_max_bytes > MAX_SPOOL_BYTES
            || self.spool_max_bytes < self.spool_max_entries as u64 * MAX_OUTCOME_BYTES
            || !(1..=60).contains(&self.outcome_retry_seconds)
            || self.control_idle_connections > 64
            || self.backend_idle_connections > self.active_requests as usize
            || !(4_096..=9_007_199_254_740_991).contains(&self.minimum_free_disk_bytes)
        {
            return Err("GATEWAY_LIMITS_INVALID");
        }
        Ok(())
    }
}

fn canonical(path: &Path) -> Result<()> {
    if !path.is_absolute()
        || path
            .components()
            .any(|component| matches!(component, std::path::Component::ParentDir))
        || path
            .canonicalize()
            .map_err(|_| "GATEWAY_PATH_UNAVAILABLE")?
            != path
    {
        return Err("GATEWAY_CANONICAL_PATH_REQUIRED");
    }
    Ok(())
}

pub fn private_directory(path: &Path) -> Result<()> {
    canonical(path)?;
    let metadata = fs::symlink_metadata(path).map_err(|_| "GATEWAY_PATH_UNAVAILABLE")?;
    if !metadata.is_dir()
        || metadata.uid() != nix::unistd::geteuid().as_raw()
        || metadata.mode() & 0o077 != 0
    {
        return Err("GATEWAY_PRIVATE_DIRECTORY_REQUIRED");
    }
    Ok(())
}

pub(crate) fn file_bytes(path: &Path, maximum: u64, private: bool) -> Result<Vec<u8>> {
    canonical(path)?;
    let file = OpenOptions::new()
        .read(true)
        .custom_flags(OFlag::O_NOFOLLOW.bits())
        .open(path)
        .map_err(|_| "GATEWAY_FILE_UNAVAILABLE")?;
    let metadata = file.metadata().map_err(|_| "GATEWAY_FILE_UNAVAILABLE")?;
    if !metadata.is_file()
        || metadata.uid() != nix::unistd::geteuid().as_raw()
        || metadata.mode() & 0o022 != 0
        || (private && metadata.mode() & 0o077 != 0)
        || metadata.len() == 0
        || metadata.len() > maximum
    {
        return Err("GATEWAY_FILE_PERMISSIONS_OR_SIZE_INVALID");
    }
    if private {
        private_directory(path.parent().ok_or("GATEWAY_PATH_INVALID")?)?;
    }
    let mut output = Vec::with_capacity(metadata.len() as usize);
    file.take(maximum + 1)
        .read_to_end(&mut output)
        .map_err(|_| "GATEWAY_FILE_UNAVAILABLE")?;
    if output.len() as u64 != metadata.len() {
        return Err("GATEWAY_FILE_CHANGED");
    }
    Ok(output)
}

pub fn secret_file(path: &Path) -> Result<Vec<u8>> {
    file_bytes(path, MAX_SECRET_BYTES, true)
}

pub fn certificate_file(path: &Path) -> Result<Vec<u8>> {
    file_bytes(path, MAX_CERTIFICATE_BYTES, false)
}

impl Configuration {
    pub fn load(path: &Path) -> Result<Self> {
        Self::load_with_environment(path, None)
    }

    // One typed owner: protected JSON, then an explicit protected env-file.
    // Ambient process variables, interpolation and shell execution have no authority.
    pub fn load_with_environment(path: &Path, environment: Option<&Path>) -> Result<Self> {
        let bytes = file_bytes(path, MAX_CONFIGURATION_BYTES, true)?;
        let configuration: Self =
            serde_json::from_slice(&bytes).map_err(|_| "GATEWAY_CONFIGURATION_INVALID")?;
        configuration.validate()?;
        let Some(environment) = environment else {
            return Ok(configuration);
        };
        let raw = file_bytes(environment, MAX_CONFIGURATION_BYTES, true)?;
        let raw = std::str::from_utf8(&raw).map_err(|_| "GATEWAY_ENVIRONMENT_INVALID")?;
        let mut value =
            serde_json::to_value(configuration).map_err(|_| "GATEWAY_ENVIRONMENT_INVALID")?;
        let mut seen = std::collections::BTreeSet::new();
        for line in raw.lines() {
            let line = line.trim();
            if line.is_empty() || line.starts_with('#') {
                continue;
            }
            let (key, input) = line.split_once('=').ok_or("GATEWAY_ENVIRONMENT_INVALID")?;
            let key = key.trim();
            let input = input.trim();
            if input.is_empty() || !seen.insert(key) {
                return Err("GATEWAY_ENVIRONMENT_INVALID");
            }
            let name = key
                .strip_prefix("PROJECT_ONE_GATEWAY_")
                .ok_or("GATEWAY_ENVIRONMENT_INVALID")?;
            if name.is_empty() || !name.bytes().all(|b| b.is_ascii_uppercase() || b == b'_') {
                return Err("GATEWAY_ENVIRONMENT_INVALID");
            }
            let name = name.to_ascii_lowercase();
            let target = if let Some(name) = name.strip_prefix("limit_") {
                value
                    .get_mut("limits")
                    .and_then(|limits| limits.get_mut(name))
            } else if matches!(name.as_str(), "limits" | "quota_policy") {
                None
            } else {
                value.get_mut(&name)
            }
            .ok_or("GATEWAY_ENVIRONMENT_INVALID")?;
            *target = if target.is_string() {
                let text = if input.starts_with('"') {
                    serde_json::from_str::<String>(input)
                        .map_err(|_| "GATEWAY_ENVIRONMENT_INVALID")?
                } else {
                    input.to_owned()
                };
                if text.is_empty() {
                    return Err("GATEWAY_ENVIRONMENT_INVALID");
                }
                serde_json::Value::String(text)
            } else {
                let parsed: serde_json::Value =
                    serde_json::from_str(input).map_err(|_| "GATEWAY_ENVIRONMENT_INVALID")?;
                if !parsed.is_number() {
                    return Err("GATEWAY_ENVIRONMENT_INVALID");
                }
                parsed
            };
        }
        let configuration: Self =
            serde_json::from_value(value).map_err(|_| "GATEWAY_ENVIRONMENT_INVALID")?;
        configuration.validate()?;
        Ok(configuration)
    }

    pub fn validate(&self) -> Result<()> {
        self.limits.validate()?;
        if let Some(policy) = &self.quota_policy {
            policy.validate()?;
        }
        if self.gateway_id.is_nil() || self.node_id.is_nil() {
            return Err("GATEWAY_CONFIGURATION_INVALID");
        }
        // The backend has no authentication of its own, so it stays on loopback; callers reach
        // the gateway's own TLS listener on whichever address the operator chose for it.
        if !self.backend_address.ip().is_loopback() || self.backend_address.port() == 0 {
            return Err("GATEWAY_LOOPBACK_BINDING_REQUIRED");
        }
        let listen = self.listen_address.ip();
        if self.listen_address.port() == 0
            || listen.is_multicast()
            || listen.is_broadcast()
            || (self.listen_address.port() == self.backend_address.port()
                && (listen.is_unspecified() || *listen == *self.backend_address.ip()))
        {
            return Err("GATEWAY_LISTEN_ADDRESS_INVALID");
        }
        let url = reqwest::Url::parse(&self.control_origin)
            .map_err(|_| "GATEWAY_CONTROL_ORIGIN_INVALID")?;
        if url.scheme() != "https"
            || url.host_str().is_none()
            || url.username() != ""
            || url.password().is_some()
            || (url.path() != "" && url.path() != "/")
            || url.query().is_some()
            || url.fragment().is_some()
            || self.control_origin.ends_with('/')
        {
            return Err("GATEWAY_CONTROL_ORIGIN_INVALID");
        }
        if !valid_scope_id(&self.site_id) || !valid_scope_id(&self.cell_id) {
            return Err("GATEWAY_SCOPE_INVALID");
        }
        private_directory(&self.state_directory)?;
        Ok(())
    }
}

fn default_scope_id() -> String {
    "default".to_owned()
}

/// The immutable site and cell identifier form, `^[a-z0-9]([a-z0-9-]{0,30}[a-z0-9])?$`.
fn valid_scope_id(value: &str) -> bool {
    let bytes = value.as_bytes();
    let edge = |b: &u8| b.is_ascii_lowercase() || b.is_ascii_digit();
    !bytes.is_empty()
        && bytes.len() <= 32
        && edge(&bytes[0])
        && edge(&bytes[bytes.len() - 1])
        && bytes.iter().all(|b| edge(b) || *b == b'-')
}

#[cfg(test)]
mod tests {
    use super::*;
    use serde_json::json;
    use std::os::unix::fs::PermissionsExt;

    fn directory() -> PathBuf {
        let root = std::env::temp_dir().join(format!("gateway-config-{}", Uuid::new_v4()));
        fs::create_dir(&root).unwrap();
        fs::set_permissions(&root, fs::Permissions::from_mode(0o700)).unwrap();
        root.canonicalize().unwrap()
    }

    #[test]
    fn defaults_and_nondefault_limits_enforce_memory_and_deadline_bounds() {
        let mut limits = Limits::default();
        assert!(limits.validate().is_ok());
        limits.active_requests = 2;
        limits.backend_idle_connections = 2;
        limits.queued_requests = 1;
        limits.request_seconds = 30;
        limits.idle_seconds = 5;
        assert!(limits.validate().is_ok());
        limits.backend_idle_connections = 3;
        assert_eq!(limits.validate(), Err("GATEWAY_LIMITS_INVALID"));
        limits.backend_idle_connections = 2;
        limits.request_memory_budget_bytes = limits.request_body_bytes;
        assert_eq!(limits.validate(), Err("GATEWAY_LIMITS_INVALID"));
        assert!(serde_json::from_str::<Limits>(r#"{"unknown":1}"#).is_err());
        assert!(
            serde_json::from_str::<Limits>(r#"{"active_requests":1,"active_requests":2}"#).is_err()
        );
    }

    #[test]
    fn protected_configuration_accepts_nondefault_port_and_rejects_public_backend() {
        let root = directory();
        let path = root.join("gateway.json");
        let mut value = json!({"gateway_id":Uuid::new_v4(),"node_id":Uuid::new_v4(),"listen_address":"127.0.0.1:19443","backend_address":"127.0.0.1:19001","control_origin":"https://admin.example.invalid:19444","control_ca_file":root.join("ca.pem"),"gateway_credential_file":root.join("credential"),"tls_certificate_file":root.join("certificate.pem"),"tls_private_key_file":root.join("key.pem"),"state_directory":root});
        fs::write(&path, serde_json::to_vec(&value).unwrap()).unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        assert_eq!(
            Configuration::load(&path).unwrap().backend_address.port(),
            19001
        );
        value["backend_address"] = json!("198.51.100.1:19001");
        fs::write(&path, serde_json::to_vec(&value).unwrap()).unwrap();
        assert!(matches!(
            Configuration::load(&path),
            Err("GATEWAY_LOOPBACK_BINDING_REQUIRED")
        ));
        fs::set_permissions(&path, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(matches!(
            Configuration::load(&path),
            Err("GATEWAY_FILE_PERMISSIONS_OR_SIZE_INVALID")
        ));
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn listener_may_leave_loopback_while_the_backend_may_not() {
        let root = directory();
        let mut value = json!({"gateway_id":Uuid::new_v4(),"node_id":Uuid::new_v4(),"listen_address":"192.0.2.10:19443","backend_address":"127.0.0.1:19001","control_origin":"https://admin.example.invalid:19444","control_ca_file":root.join("ca.pem"),"gateway_credential_file":root.join("credential"),"tls_certificate_file":root.join("certificate.pem"),"tls_private_key_file":root.join("key.pem"),"state_directory":root});
        let check = |value: &serde_json::Value| {
            serde_json::from_value::<Configuration>(value.clone())
                .unwrap()
                .validate()
        };
        assert_eq!(check(&value), Ok(()));
        for (listen, expected) in [
            ("0.0.0.0:19443", Ok(())),
            ("127.0.0.1:19443", Ok(())),
            ("0.0.0.0:19001", Err("GATEWAY_LISTEN_ADDRESS_INVALID")),
            ("127.0.0.1:19001", Err("GATEWAY_LISTEN_ADDRESS_INVALID")),
            ("192.0.2.10:19001", Ok(())),
            ("192.0.2.10:0", Err("GATEWAY_LISTEN_ADDRESS_INVALID")),
            ("224.0.0.1:19443", Err("GATEWAY_LISTEN_ADDRESS_INVALID")),
            (
                "255.255.255.255:19443",
                Err("GATEWAY_LISTEN_ADDRESS_INVALID"),
            ),
        ] {
            value["listen_address"] = json!(listen);
            assert_eq!(check(&value), expected, "{listen}");
        }
        value["listen_address"] = json!("192.0.2.10:19443");
        for backend in ["0.0.0.0:19001", "192.0.2.10:19001", "127.0.0.1:0"] {
            value["backend_address"] = json!(backend);
            assert_eq!(
                check(&value),
                Err("GATEWAY_LOOPBACK_BINDING_REQUIRED"),
                "{backend}"
            );
        }
        value["backend_address"] = json!("127.0.0.1:19001");
        value["node_id"] = json!(Uuid::nil());
        assert_eq!(check(&value), Err("GATEWAY_CONFIGURATION_INVALID"));
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn protected_environment_uses_same_typed_owner_and_rejects_false_duplicates_and_secrets() {
        let root = directory();
        let path = root.join("gateway.json");
        let env = root.join("gateway.env");
        let value = json!({"gateway_id":Uuid::new_v4(),"node_id":Uuid::new_v4(),"listen_address":"127.0.0.1:19443","backend_address":"127.0.0.1:18000","control_origin":"https://admin.example.invalid:19444","control_ca_file":root.join("ca.pem"),"gateway_credential_file":root.join("credential"),"tls_certificate_file":root.join("certificate.pem"),"tls_private_key_file":root.join("key.pem"),"state_directory":root});
        fs::write(&path, serde_json::to_vec(&value).unwrap()).unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        fs::write(&env,b"# protected explicit overrides\nPROJECT_ONE_GATEWAY_LISTEN_ADDRESS=127.0.0.1:19445\nPROJECT_ONE_GATEWAY_LIMIT_ACTIVE_REQUESTS=2\nPROJECT_ONE_GATEWAY_LIMIT_BACKEND_IDLE_CONNECTIONS=2\nPROJECT_ONE_GATEWAY_LIMIT_QUEUED_REQUESTS=0\nPROJECT_ONE_GATEWAY_LIMIT_REQUEST_SECONDS=30\nPROJECT_ONE_GATEWAY_LIMIT_DEFAULT_TEMPERATURE=0.5\n").unwrap();
        fs::set_permissions(&env, fs::Permissions::from_mode(0o600)).unwrap();
        let c = Configuration::load_with_environment(&path, Some(&env)).unwrap();
        assert_eq!(c.listen_address.port(), 19445);
        assert_eq!(c.limits.active_requests, 2);
        assert_eq!(c.limits.queued_requests, 0);
        assert_eq!(c.limits.request_seconds, 30);
        assert_eq!(c.limits.default_temperature, 0.5);
        assert_eq!(
            Configuration::load(&path).unwrap().listen_address.port(),
            19443
        );
        for invalid in [
            "PROJECT_ONE_GATEWAY_LIMIT_ACTIVE_REQUESTS=false",
            "PROJECT_ONE_GATEWAY_LIMIT_QUEUED_REQUESTS=-1",
            "PROJECT_ONE_GATEWAY_LIMIT_ACTIVE_REQUESTS=1\nPROJECT_ONE_GATEWAY_LIMIT_ACTIVE_REQUESTS=2",
            "PROJECT_ONE_GATEWAY_UNKNOWN=1",
            "PROJECT_ONE_GATEWAY_GATEWAY_CREDENTIAL=secret",
            "PROJECT_ONE_GATEWAY_LISTEN_ADDRESS=",
            "PROJECT_ONE_GATEWAY_LIMIT_REQUEST_SECONDS=null",
            "PROJECT_ONE_GATEWAY_BACKEND_ADDRESS=203.0.113.1:1234",
            "PROJECT_ONE_GATEWAY_LIMIT_DEFAULT_TEMPERATURE=NaN",
            "export PROJECT_ONE_GATEWAY_LIMIT_ACTIVE_REQUESTS=1",
        ] {
            fs::write(&env, invalid).unwrap();
            assert!(Configuration::load_with_environment(&path, Some(&env)).is_err());
        }
        fs::write(&env, b"PROJECT_ONE_GATEWAY_LIMIT_REQUEST_SECONDS=30").unwrap();
        fs::set_permissions(&env, fs::Permissions::from_mode(0o644)).unwrap();
        assert!(matches!(
            Configuration::load_with_environment(&path, Some(&env)),
            Err("GATEWAY_FILE_PERMISSIONS_OR_SIZE_INVALID")
        ));
        fs::remove_dir_all(root).unwrap();
    }

    #[test]
    fn secret_symlink_and_shared_parent_are_rejected_without_echoing_content() {
        let root = directory();
        let path = root.join("secret");
        fs::write(&path, b"synthetic-credential-must-not-appear-in-errors").unwrap();
        fs::set_permissions(&path, fs::Permissions::from_mode(0o600)).unwrap();
        assert!(secret_file(&path).is_ok());
        let link = root.join("link");
        std::os::unix::fs::symlink(&path, &link).unwrap();
        assert_eq!(secret_file(&link), Err("GATEWAY_CANONICAL_PATH_REQUIRED"));
        fs::set_permissions(&root, fs::Permissions::from_mode(0o755)).unwrap();
        assert_eq!(
            secret_file(&path),
            Err("GATEWAY_PRIVATE_DIRECTORY_REQUIRED")
        );
        fs::remove_dir_all(root).unwrap();
    }
}
