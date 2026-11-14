"""Run the installed offline encoder with one bounded JSON request on stdin."""
from pathlib import Path
import sys

sys.path.insert(0, str(Path(__file__).resolve().parent))

if __name__ == '__main__':
    from projectone_knowledge.process_guard import arm
    try:
        arm()
    except ValueError:
        sys.stderr.write('{"error":"KNOWLEDGE_WORKER_REQUEST_INVALID"}')
        raise SystemExit(2)
    from projectone_knowledge.boundary import main
    main()
