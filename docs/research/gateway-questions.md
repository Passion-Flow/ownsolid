# Gateway lane boundaries

## GW-105 PostgreSQL key purpose

The existing schema does not record ordinary versus operation-only key
purpose. The verified candidate needs a nullable column and the matching
fresh and restored fingerprints in the shared database catalog. Reuse
the existing rotation and admission contracts. Do not infer purpose
from one-way MAC bytes or weaken the catalog check. Keep the change a
candidate until the database owner lands the migration and both
fingerprints together. The [key-purpose note](gateway-key-purpose.md)
has the exact verification and patch location.
