"""Codec checks only: controlled HTTPX replies, no DB, model approval or route."""
import copy,json,sys,unittest
from pathlib import Path
from uuid import UUID
import httpx
root=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(root/'sdk/gen/python'))
from ownsolid_sdk.models.client import Client
from ownsolid_sdk.models.types import Unset
from ownsolid_sdk.models.api.default import get_admin_model,get_admin_historical_model_version,list_admin_model_version_history,list_admin_models
from ownsolid_sdk.models.models.list_admin_model_version_history_state import ListAdminModelVersionHistoryState

fixtures=Path(__file__).resolve().parent/'fixtures'
ids=json.loads((fixtures/'ids.json').read_text())
family,workspace,ready_id,retired_id=(UUID(ids[name]) for name in ['family','workspace','ready','retired'])

class HistoryWireTests(unittest.TestCase):
    def setUp(self):
        self.calls=[];self.with_family=False;self.denied=False
        self.ready=json.loads((fixtures/'ready.json').read_text())
        self.retired=json.loads((fixtures/'retired.json').read_text())
        self.page=json.loads((fixtures/'page.json').read_text())
        self.client=Client(base_url='https://admin.example.invalid')
        self.transport_client=httpx.Client(base_url='https://admin.example.invalid',transport=httpx.MockTransport(self.respond))
        self.client.set_httpx_client(self.transport_client)
        self.addCleanup(self.transport_client.close)
    def respond(self,request):
        self.assertEqual(request.method,'GET');self.assertEqual(request.content,b'')
        self.calls.append(request)
        q=request.url.params
        if self.denied or q.get('limit')=='0' or ('cursor' in q and q['cursor']==''):
            status,code=(403,'ACCESS_DENIED') if self.denied else (400,'VALIDATION_FAILED')
            return httpx.Response(status,json={'type':'about:blank','title':code,'status':status,'code':code,'request_id':ids['ready']})
        if request.url.path.endswith('/model-families/'+ids['family']+'/versions'):
            self.assertEqual(q['workspace_id'],ids['workspace'])
            return httpx.Response(200,json={'data':self.page})
        if request.url.path.endswith('/versions/'+ids['retired']):
            self.assertEqual(q['workspace_id'],ids['workspace'])
            return httpx.Response(200,json={'data':self.retired})
        if request.url.path.endswith('/models/'+ids['ready']):
            row=copy.deepcopy(self.ready)
            if self.with_family:row['family_id']=ids['family']
            return httpx.Response(200,json={'data':row})
        if request.url.path.endswith('/admin/models'):
            return httpx.Response(200,json={'data':{'items':[self.ready]}})
        self.fail('Unexpected prototype request: '+str(request.url))
    def history(self,**kwargs):
        return list_admin_model_version_history.sync_detailed('default','default',family,client=self.client,workspace_id=workspace,**kwargs)
    def test_omitted_defaults_and_exact_version_rows(self):
        response=self.history();self.assertEqual(response.status_code,200)
        self.assertEqual(dict(self.calls[-1].url.params),{'workspace_id':ids['workspace']})
        page=response.parsed.data
        self.assertEqual([str(x.id) for x in page.items],[ids['ready'],ids['retired']])
        self.assertEqual([x.state for x in page.items],['ready','retired'])
        self.assertEqual(page.items[0].catalog_digest,page.items[1].catalog_digest)
        self.assertIsNone(page.next_cursor);self.assertIn('next_cursor',page.to_dict())
    def test_explicit_page_filter_and_opaque_cursor(self):
        cursor='opaque+/=%\u754c'
        response=self.history(limit=7,cursor=cursor,state=ListAdminModelVersionHistoryState.RETIRED)
        self.assertEqual(response.status_code,200)
        self.assertEqual(dict(self.calls[-1].url.params),{'workspace_id':ids['workspace'],'limit':'7','cursor':cursor,'state':'retired'})
    def test_explicit_zero_and_empty_cursor_are_sent(self):
        for options,key,value in [({'limit':0},'limit','0'),({'cursor':''},'cursor','')]:
            with self.subTest(options=options):
                response=self.history(**options);self.assertEqual(response.status_code,400)
                self.assertEqual(self.calls[-1].url.params[key],value)
                self.assertEqual(response.parsed.code.value,'VALIDATION_FAILED')
        self.assertEqual(len(self.calls),2)
    def test_old_ready_detail_keeps_family_unset(self):
        response=get_admin_model.sync_detailed('default','default',ready_id,client=self.client)
        self.assertEqual(response.status_code,200);self.assertIsInstance(response.parsed.data.family_id,Unset)
        self.assertNotIn('family_id',response.parsed.data.to_dict())
        self.assertEqual(response.parsed.data.state,'ready')
    def test_owner_supplied_family_discovery_is_preserved(self):
        self.with_family=True
        response=get_admin_model.sync_detailed('default','default',ready_id,client=self.client)
        self.assertEqual(response.parsed.data.family_id,family)
        self.history();self.assertEqual(len(self.calls),2)
    def test_retired_detail_uses_separate_metadata_type(self):
        response=get_admin_historical_model_version.sync_detailed('default','default',family,retired_id,client=self.client,workspace_id=workspace)
        self.assertEqual(response.status_code,200);value=response.parsed.data
        self.assertEqual(value.id,retired_id);self.assertEqual(value.family_id,family)
        self.assertEqual(value.state,'retired')
        self.assertEqual(value.manifest.to_dict(),self.retired['manifest'])
        self.assertIsInstance(value.dataset_lineage,Unset)
    def test_current_list_shape_and_problem_codec(self):
        response=list_admin_models.sync_detailed('default','default',client=self.client,workspace_id=workspace)
        self.assertEqual(response.status_code,200);self.assertEqual(response.parsed.data.items[0].state,'ready')
        self.assertNotIn('family_id',response.parsed.data.items[0].to_dict())
        self.denied=True;response=self.history();self.assertEqual(response.status_code,403)
        self.assertEqual(response.parsed.code.value,'ACCESS_DENIED');self.assertEqual(len(self.calls),2)

if __name__=='__main__':unittest.main(verbosity=2)
