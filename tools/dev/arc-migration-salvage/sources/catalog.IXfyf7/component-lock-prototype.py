import os
import subprocess
import time

command = ["docker", "exec", "-i", os.environ["PROJECT_ONE_FIXTURE_CONTAINER"],
           "psql", "-XqAt", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", "ownsolid_test"]

def run(sql):
    return subprocess.run(command, input=sql, text=True, capture_output=True, check=True).stdout.strip()

run("""
CREATE TABLE proof_tenants(id int PRIMARY KEY, status text);
CREATE TABLE proof_installation(id int PRIMARY KEY, tenant_id int NOT NULL REFERENCES proof_tenants, bootstrap_complete bool);
CREATE TABLE proof_nodes(id int PRIMARY KEY, tenant_id int NOT NULL REFERENCES proof_tenants, workspace_id int, status text, credential_hash bytea);
INSERT INTO proof_tenants VALUES (1,'active'), (2,'active');
INSERT INTO proof_installation VALUES(1,1,true);
INSERT INTO proof_nodes VALUES(1,1,1,'active',decode('01','hex')), (2,2,1,'active',decode('02','hex'));
""")
query = """
WITH installed AS MATERIALIZED (
  SELECT i.tenant_id FROM proof_installation i JOIN proof_tenants t ON t.id=i.tenant_id
  WHERE i.id=1 AND i.bootstrap_complete AND t.status='active' AND i.tenant_id={tenant}
  FOR SHARE OF i,t
)
SELECT n.status, encode(n.credential_hash,'hex')
FROM installed CROSS JOIN LATERAL (
  SELECT n.* FROM proof_nodes n
  WHERE n.tenant_id=installed.tenant_id AND n.workspace_id=1 AND n.id={node}
  FOR UPDATE OF n
) n;
"""
plan = run("SET enable_seqscan=off; EXPLAIN (COSTS OFF) "+query.format(tenant=1,node=1))
print(plan)
assert "CTE Scan on installed" in plan and plan.count("LockRows") == 2
assert run(query.format(tenant=2,node=2)) == ""

with subprocess.Popen(command, stdin=subprocess.PIPE, stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as holder:
    holder.stdin.write("BEGIN; UPDATE proof_nodes SET credential_hash=decode('03','hex') WHERE id=1; SELECT 'held';\n")
    holder.stdin.flush()
    assert holder.stdout.readline().strip() == "held"
    with subprocess.Popen(command + ["-c", query.format(tenant=1,node=1)], stdout=subprocess.PIPE, stderr=subprocess.PIPE, text=True) as reader:
        for _ in range(40):
            waiting = run("SELECT count(*) FROM pg_stat_activity WHERE wait_event_type='Lock' AND cardinality(pg_blocking_pids(pid))>0 AND query LIKE '%%proof_nodes%%';")
            if waiting == "1":
                break
            time.sleep(0.05)
        else:
            raise RuntimeError("reader did not wait on the held node")
        # It has already acquired the installation's share lock.
        assert run("""BEGIN;
          DO $$ BEGIN
            PERFORM id FROM proof_installation WHERE id=1 FOR UPDATE NOWAIT;
            RAISE EXCEPTION 'installation unexpectedly unlocked';
          EXCEPTION WHEN lock_not_available THEN NULL;
          END $$;
          ROLLBACK;
        """) == ""
        holder.stdin.write("COMMIT;\n")
        holder.stdin.flush()
        out, err = reader.communicate(timeout=5)
        assert reader.returncode == 0 and out.strip() == "active|03", (out, err)
    holder.stdin.close()
print("Foreign tenant refused. Installation was locked before the waiting node read. Credential read the committed successor row.")
