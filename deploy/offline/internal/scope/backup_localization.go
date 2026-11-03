package scope

import wire "github.com/Passion-Flow/ownsolid/api/release/v1"

// Only the exact original journal-bound hash can select historical absence of
// this optional task dependency. New plans always bind their prepared locale
// role; no external flag, changed context or arbitrary owner is normalized.
func recordedBackupLocalization(p BackupPlan, originalSHA256 string) BackupPlan {
	if wire.Digest(encode(p)) == originalSHA256 {
		return p
	}
	legacy := p
	legacy.Jobs = make(map[string]BackupJob, len(p.Jobs))
	for key, job := range p.Jobs {
		if job.Store != nil {
			task := *job.Store
			task.LocalizationOwner = nil
			job.Store = &task
		}
		legacy.Jobs[key] = job
	}
	if wire.Digest(encode(legacy)) == originalSHA256 {
		return legacy
	}
	return p
}
