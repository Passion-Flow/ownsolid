package compose_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// Docker Desktop changes ownership at a host bind mount's root. Stage only the
// explicit synthetic host inventory into a new native source volume. The real
// static installer still verifies the complete readonly native source and target.
func stagePrivateConfigurationSource(t *testing.T, source, image, scope, operation, fixture, arch string, uid, gid int, inventory []map[string]any) string {
	t.Helper()
	volume := "projectone-" + scope + "-control-config-source"
	name := "projectone-" + scope + "-source-stage"
	info, e := os.Lstat(source)
	if e != nil {
		t.Fatal("PRIVATE_SOURCE_HOST_DIRECTORY_UNAVAILABLE")
	}
	owner, ok := info.Sys().(*syscall.Stat_t)
	if info.Mode() != os.ModeDir|0700 || !ok || int(owner.Uid) != uid || int(owner.Gid) != gid {
		t.Fatal("PRIVATE_SOURCE_HOST_DIRECTORY_REFUSED")
	}
	payload := map[string][]byte{}
	for _, file := range inventory {
		path := filepath.Join(source, file["name"].(string))
		info, e := os.Lstat(path)
		if e != nil {
			t.Fatal("PRIVATE_SOURCE_HOST_FILE_UNAVAILABLE")
		}
		owner, ok := info.Sys().(*syscall.Stat_t)
		if info.Mode() != 0600 || !ok || int(owner.Uid) != uid || int(owner.Gid) != gid || owner.Nlink != 1 {
			t.Fatal("PRIVATE_SOURCE_HOST_FILE_REFUSED")
		}
		payload[file["name"].(string)], e = os.ReadFile(path)
		if e != nil {
			t.Fatal("PRIVATE_SOURCE_HOST_FILE_UNAVAILABLE")
		}
	}
	intent, _ := json.Marshal(map[string]any{"volume": volume, "container": name, "scope": scope, "operation": operation, "fixture": fixture, "uid": uid, "gid": gid, "files": inventory})
	intentPath := filepath.Join(filepath.Dir(source), "private-source-staging.json")
	f, e := os.OpenFile(intentPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if e != nil {
		t.Fatal("PRIVATE_SOURCE_NEW_INTENT_REQUIRED")
	}
	_, e = f.Write(intent)
	syncError := f.Sync()
	closeError := f.Close()
	if e != nil || syncError != nil || closeError != nil {
		t.Fatal("PRIVATE_SOURCE_INTENT_DURABILITY_FAILED")
	}
	if exec.Command("docker", "volume", "inspect", volume).Run() == nil {
		t.Fatal("PRIVATE_SOURCE_EXISTING_VOLUME_REFUSED")
	}
	t.Cleanup(func() {
		var values []struct {
			Name, Driver, Scope string
			Labels, Options     map[string]string
		}
		raw, e := exec.Command("docker", "volume", "inspect", volume).CombinedOutput()
		if e != nil {
			if strings.Contains(strings.ToLower(string(raw)), "no such volume") {
				return
			}
			t.Error("PRIVATE_SOURCE_CLEANUP_UNCONFIRMED")
			return
		}
		if json.Unmarshal(raw, &values) != nil || len(values) != 1 || values[0].Name != volume || values[0].Driver != "local" || values[0].Scope != "local" || len(values[0].Options) != 0 || values[0].Labels["projectone.scope"] != fixture || values[0].Labels["io.projectone.isolated-scope"] != scope || values[0].Labels["io.projectone.operation"] != operation || values[0].Labels["io.projectone.role"] != "control-config-source" {
			t.Error("PRIVATE_SOURCE_CLEANUP_OWNER_REFUSED")
			return
		}
		consumers, e := exec.Command("docker", "ps", "--all", "--quiet", "--filter", "volume="+volume).Output()
		if e != nil || len(bytes.TrimSpace(consumers)) != 0 {
			t.Error("PRIVATE_SOURCE_CLEANUP_CONSUMER_REMAINS")
			return
		}
		if exec.Command("docker", "volume", "rm", volume).Run() != nil {
			t.Error("PRIVATE_SOURCE_CLEANUP_FAILED")
		}
	})
	args := []string{"volume", "create", "--driver", "local", "--label", "projectone.owner=mvp-development", "--label", "projectone.scope=" + fixture, "--label", "io.projectone.isolated-scope=" + scope, "--label", "io.projectone.operation=" + operation, "--label", "io.projectone.role=control-config-source", volume}
	if exec.Command("docker", args...).Run() != nil {
		t.Fatal("PRIVATE_SOURCE_CREATE_UNCERTAIN")
	}
	const script = `import sys,json,os,base64,hashlib,stat
d=json.load(sys.stdin);assert set(d)=={'uid','gid','files','inventory'}
assert os.listdir('/stage')==[]
root='/stage/payload';os.mkdir(root,0o700);os.chown(root,d['uid'],d['gid'])
assert set(d['files'])=={f['name'] for f in d['inventory']}
for item in d['inventory']:
 name=item['name'];assert name and os.path.basename(name)==name and '\\' not in name
 raw=base64.b64decode(d['files'][name],validate=True)
 assert len(raw)==item['bytes'] and hashlib.sha256(raw).hexdigest()==item['sha256']
 p=root+'/'+name
 with open(p,'xb') as f:
  os.fchmod(f.fileno(),0o600);os.fchown(f.fileno(),d['uid'],d['gid']);f.write(raw);f.flush();os.fsync(f.fileno())
 s=os.lstat(p);assert stat.S_IMODE(s.st_mode)==0o600 and s.st_uid==d['uid'] and s.st_gid==d['gid'] and s.st_nlink==1
 assert hashlib.sha256(open(p,'rb').read()).hexdigest()==item['sha256']
fd=os.open(root,os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
fd=os.open('/stage',os.O_RDONLY|os.O_DIRECTORY);os.fsync(fd);os.close(fd)
print('PRIVATE_SOURCE_STAGED')`
	body, _ := json.Marshal(map[string]any{"uid": uid, "gid": gid, "files": payload, "inventory": inventory})
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	args = []string{"run", "--rm", "--name", name, "--platform", "linux/" + arch, "--pull", "never", "--network", "none", "--user", "0:0", "--read-only", "--cap-drop", "ALL", "--cap-add", "CHOWN", "--cap-add", "DAC_OVERRIDE", "--security-opt", "no-new-privileges:true", "--memory", "64m", "--memory-swap", "64m", "--cpus", "1", "--pids-limit", "16", "--interactive", "--label", "io.projectone.isolated-scope=" + scope, "--label", "io.projectone.operation=" + operation, "--mount", "type=volume,src=" + volume + ",dst=/stage,volume-nocopy", "--entrypoint", "/usr/local/bin/python3.12", image, "-I", "-B", "-c", script}
	command := exec.CommandContext(ctx, "docker", args...)
	command.Stdin = bytes.NewReader(body)
	raw, e := command.Output()
	if e != nil || strings.TrimSpace(string(raw)) != "PRIVATE_SOURCE_STAGED" {
		t.Fatal("PRIVATE_SOURCE_STAGE_FAILED")
	}
	return "type=volume,src=" + volume + ",dst=/source,volume-nocopy,volume-subpath=payload,readonly"
}
