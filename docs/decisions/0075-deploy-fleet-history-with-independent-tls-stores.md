# Deploy fleet history with independent TLS stores

The installed Control process-metrics transport accepts HTTP. Fleet history
requires HTTPS for a remote raw or long origin. Converting the existing metrics
listener to TLS would break process metrics and its health probes.

Give fleet history an opt-in installer command and a separately owned Compose
project with a raw store, a long store and vmalert. Reuse the existing protected
file reader, operation lock, atomic writer and operator audit format. Keep image
references immutable and load them before applying. Use a private internal
network and numeric addresses for inter-service traffic. Attach a separate
publication bridge because Docker does not publish ports from internal-only
containers. Expose only the configured private HTTPS store
ports and a loopback evaluator health port.

Validate certificates, applicable CA bundles, leaf names, rules and policy
origins before changing a running project. Mount immutable input generations
and retain separate store data. Stop both clients and stores before changing
trust, then restart the three services. A retry after interruption uses the
same owned project and data directories. Keep the original inputs for recovery.

The deployment emits the checked monitoring-policy and Control CA file paths.
The existing install command accepts their parent directory through the optional
telemetry.fleet_history_control_directory field. It copies protected outputs,
selects the existing monitoring_config_file and process CA file, and restarts
Control on apply. Private host origins must be reachable from Control's container.
Public history dispatch stays with
OBS and DEV. This deployment command does not complete those product entrances
or qualify a production release inventory.

See the [research and acceptance requirements](../research/telemetry-retention-tiers.md#installer-continuation).
