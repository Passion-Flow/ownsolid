{{- define "ownsolid.controlName" -}}
{{- printf "%s-control" .Release.Name -}}
{{- end -}}

{{- define "ownsolid.controlSelector" -}}
app.kubernetes.io/name: ownsolid-control
app.kubernetes.io/instance: {{ .Release.Name | quote }}
{{- end -}}

{{- define "ownsolid.controlImage" -}}
{{- if not (regexMatch "^[a-f0-9]{64}$" (.Values.release.manifestSHA256 | toString)) -}}
{{- fail "HELM_RELEASE_DIGEST_REQUIRED: generate release.manifestSHA256 from the verified signed release" -}}
{{- end -}}
{{- $digest := .Values.images.control | toString -}}
{{- if not (regexMatch "^sha256:[a-f0-9]{64}$" $digest) -}}
{{- fail "HELM_IMAGE_DIGEST_REQUIRED: generate images.control from the verified signed release" -}}
{{- end -}}
{{- $authority := .Values.registry.address | toString -}}
{{- $octet := "(25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])" -}}
{{- $private := printf "(10[.]%s[.]|172[.](1[6-9]|2[0-9]|3[01])[.]|192[.]168[.])" $octet -}}
{{- if not (regexMatch (printf "^%s%s[.]%s:[1-9][0-9]{3,4}$" $private $octet $octet) $authority) -}}
{{- fail "HELM_REGISTRY_ADDRESS_INVALID: set the signed Registry site's private IPv4 address and port" -}}
{{- end -}}
{{- $port := last (splitList ":" $authority) | int -}}
{{- if or (lt $port 1024) (gt $port 65535) -}}
{{- fail "HELM_REGISTRY_ADDRESS_INVALID: the Registry port must be between 1024 and 65535" -}}
{{- end -}}
{{- printf "%s/ownsolid/release/control-image@%s" $authority $digest -}}
{{- end -}}

{{- define "ownsolid.runtimeFiles" -}}
{{- $files := list "runtime-dsn" "auth-hmac" "mail-keyring.json" "browser-ca.pem" "postgres-ca.pem" "smtp-ca.pem" "license-trust.json" -}}
{{- if .Values.smtp.username -}}
{{- $files = append $files "smtp-password" -}}
{{- end -}}
{{- toJson $files -}}
{{- end -}}

{{- define "ownsolid.controlConfiguration" -}}
topology:
  size: single-node
server:
  transport_mode: direct_https
  listen_address: 0.0.0.0:8443
  admin_origin: {{ printf "https://%s" .Values.ingress.adminHost | quote }}
  portal_origin: {{ printf "https://%s" .Values.ingress.portalHost | quote }}
  tls_certificate_file: /run/ownsolid/private/tls.crt
  tls_private_key_file: /run/ownsolid/private/tls.key
  admin_assets: /opt/projectone/admin
  portal_assets: /opt/projectone/portal
database:
  dsn_file: /run/ownsolid/private/runtime-dsn
security:
  auth_hmac_key_file: /run/ownsolid/private/auth-hmac
  mail_keyring_file: /run/ownsolid/private/mail-keyring.json
  password_blocklist_file: /opt/projectone/security/password-blocklist.txt
licensing:
  trust_file: /run/ownsolid/private/license-trust.json
smtp:
  host: {{ .Values.smtp.host | quote }}
  port: {{ .Values.smtp.port }}
  from_address: {{ .Values.smtp.fromAddress | quote }}
  tls_mode: {{ .Values.smtp.tlsMode | quote }}
  ca_file: /run/ownsolid/private/smtp-ca.pem
  {{- if .Values.smtp.username }}
  username: {{ .Values.smtp.username | quote }}
  password_file: /run/ownsolid/private/smtp-password
  {{- end }}
models:
  artifact_root: /var/lib/ownsolid/models
{{- end -}}

{{- define "ownsolid.controlContainerSecurity" -}}
allowPrivilegeEscalation: false
readOnlyRootFilesystem: true
capabilities:
  drop: ["ALL"]
{{- end -}}

{{- define "ownsolid.controlHealth" -}}
exec:
  command:
    - /usr/local/bin/control
    - health-probe
    - --config
    - /run/ownsolid/private/control.yaml
    - --audience
    - admin
    - --kind
    - {{ . | quote }}
    - --dial-address
    - "127.0.0.1:8443"
    - --timeout-seconds
    - "3"
    - --ca-file
    - /run/ownsolid/private/browser-ca.pem
timeoutSeconds: 5
{{- end -}}
