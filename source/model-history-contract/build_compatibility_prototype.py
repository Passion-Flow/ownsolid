"""Add existing ready-read controls and an optional owner-supplied family reference."""
from pathlib import Path
import copy,hashlib,json
root=Path(__file__).resolve().parent
work=root/'work'
p=root/'prototype/v1/models/openapi.json'
d=json.loads(p.read_text())
base=json.loads((work/'api/openapi/v1/models/openapi.json').read_text())
for path in ['/api/v1/sites/{site_id}/cells/{cell_id}/admin/models','/api/v1/sites/{site_id}/cells/{cell_id}/admin/models/{model_id}']:
    d['paths'][path]=copy.deepcopy(base['paths'][path])
if 'parameters' in base['components']:
    d['components']['parameters']=copy.deepcopy(base['components']['parameters'])
family=copy.deepcopy(d['components']['schemas']['ModelVersionHistoryEntry']['properties']['family_id'])
family['description']+=' Omit until the owner establishes membership; clients must never guess a family.'
d['components']['schemas']['AdminModelDetail']['properties']['family_id']=family
for name in ['ModelVersion','ModelCatalog','DerivedModelCatalog','ModelDatasetLineage']:
    assert d['components']['schemas'][name]==base['components']['schemas'][name]
assert d['components']['schemas']['AdminModelDetail']['required']==base['components']['schemas']['AdminModelDetail']['required']
assert 'family_id' not in d['components']['schemas']['AdminModelDetail']['required']
for name,value in base['components']['schemas']['AdminModelDetail']['properties'].items():
    assert d['components']['schemas']['AdminModelDetail']['properties'][name]==value
assert len(d['paths'])==4
assert all(set(item).intersection({'get','post','put','patch','delete'})=={'get'} for item in d['paths'].values())
p.write_text(json.dumps(d,indent=2)+'\n')
p=root/'model-owner-decisions.json';decisions=json.loads(p.read_text());decisions['prototype_sha256']=hashlib.sha256((root/'prototype/v1/models/openapi.json').read_bytes()).hexdigest();p.write_text(json.dumps(decisions,indent=2)+'\n')
print('COMPATIBILITY_PROTOTYPE_PREPARED',decisions['prototype_sha256'])
