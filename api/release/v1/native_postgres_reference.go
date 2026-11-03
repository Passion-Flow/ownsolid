package release

// NativePostgreSQLBackupReference is a safe reference to an original, verified
// native backup. It contains no configuration, directory identity or credential.
// SourceManifestSHA256 is the original native request declaration; the scope
// must independently compare it with its already verified signed release.
// Valid checks the reference's shape only. The native owner must still verify
// the original candidate/completion/readback and current immutable payload.
type NativePostgreSQLBackupReference struct {
	FormatVersion        int                                `json:"format_version"`
	ApplicationProfile   string                             `json:"application_profile"`
	SourceManifestSHA256 string                             `json:"source_manifest_sha256"`
	SchemaVersion        int                                `json:"schema_version"`
	TableCount           int                                `json:"table_count"`
	RequestSHA256        string                             `json:"request_sha256"`
	CandidateSHA256      string                             `json:"candidate_sha256"`
	CompletionSHA256     string                             `json:"completion_sha256"`
	ReadbackSHA256       string                             `json:"readback_sha256"`
	MVPSourceSHA256      string                             `json:"mvp_source_sha256"`
	RegistryConfigured   bool                               `json:"registry_configured"`
	Registry             *NativePostgreSQLRegistryReference `json:"registry,omitempty"`
}

// ReceiptSHA256 hashes the canonical original OperatorSnapshotReceipt JSON;
// the other values are copied from that verified original receipt/payload.
type NativePostgreSQLRegistryReference struct {
	ReceiptSHA256     string `json:"receipt_sha256"`
	ManifestSHA256    string `json:"manifest_sha256"`
	PayloadTreeSHA256 string `json:"payload_tree_sha256"`
	Pins              int    `json:"pins"`
	Files             int    `json:"files"`
	Bytes             int64  `json:"bytes"`
}

func (r NativePostgreSQLRegistryReference) Valid() bool {
	return validNativeReferenceDigests(r.ReceiptSHA256, r.ManifestSHA256, r.PayloadTreeSHA256) && r.Pins >= 0 && r.Files >= 0 && r.Bytes >= 0
}

func (r NativePostgreSQLBackupReference) Valid() bool {
	return r.FormatVersion == 1 && r.ApplicationProfile == MVPPostgreSQLCPUApplicationProfile && r.SchemaVersion == MVPPostgreSQLCPUV1SchemaVersion && r.TableCount == MVPPostgreSQLCPUV1TableCount && validNativeReferenceDigests(r.SourceManifestSHA256, r.RequestSHA256, r.CandidateSHA256, r.CompletionSHA256, r.ReadbackSHA256, r.MVPSourceSHA256) && r.RegistryConfigured == (r.Registry != nil) && (r.Registry == nil || r.Registry.Valid())
}

// NativePostgreSQLRestoreReference refers to one complete original native
// restore and its two independent readonly observations. A backup reference on
// its own never establishes imports, revocation, audit, or restore completion.
// The native owner remains the sole authority for those effects and their order.
type NativePostgreSQLRestoreReference struct {
	FormatVersion       int                                      `json:"format_version"`
	ApplicationProfile  string                                   `json:"application_profile"`
	Backup              NativePostgreSQLBackupReference          `json:"backup"`
	RequestSHA256       string                                   `json:"request_sha256"`
	ManifestSHA256      string                                   `json:"manifest_sha256"`
	IntentSHA256        string                                   `json:"intent_sha256"`
	FilesSHA256         string                                   `json:"files_sha256"`
	CompletionSHA256    string                                   `json:"completion_sha256"`
	FilesReadbackSHA256 string                                   `json:"files_readback_sha256"`
	ObservationSHA256   string                                   `json:"observation_sha256"`
	TargetBindingSHA256 string                                   `json:"target_binding_sha256"`
	PostDataSHA256      string                                   `json:"post_data_sha256"`
	Registry            *NativePostgreSQLRegistryImportReference `json:"registry,omitempty"`
}

// ImportReceiptSHA256 hashes the canonical original
// OperatorSnapshotImportReceipt JSON, after its complete import/readback.
type NativePostgreSQLRegistryImportReference struct {
	SnapshotReceiptSHA256 string `json:"snapshot_receipt_sha256"`
	ImportReceiptSHA256   string `json:"import_receipt_sha256"`
	ReadbackSHA256        string `json:"readback_sha256"`
}

func (r NativePostgreSQLRestoreReference) Valid() bool {
	if r.FormatVersion != 1 || r.ApplicationProfile != MVPPostgreSQLCPUApplicationProfile || !r.Backup.Valid() || r.ManifestSHA256 != r.Backup.CandidateSHA256 || !validNativeReferenceDigests(r.RequestSHA256, r.ManifestSHA256, r.IntentSHA256, r.FilesSHA256, r.CompletionSHA256, r.FilesReadbackSHA256, r.ObservationSHA256, r.TargetBindingSHA256, r.PostDataSHA256) || (r.Registry != nil) != r.Backup.RegistryConfigured {
		return false
	}
	return r.Registry == nil || r.Registry.SnapshotReceiptSHA256 == r.Backup.Registry.ReceiptSHA256 && validNativeReferenceDigests(r.Registry.SnapshotReceiptSHA256, r.Registry.ImportReceiptSHA256, r.Registry.ReadbackSHA256)
}

func validNativeReferenceDigests(values ...string) bool {
	for _, value := range values {
		if !ValidSHA256(value) {
			return false
		}
	}
	return true
}
