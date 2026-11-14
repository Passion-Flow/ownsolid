"""Controlled generated-client checks; no service, authorization or SQL fixture."""
import asyncio
import json
from pathlib import Path
import sys
import unittest
import httpx
root=Path(__file__).resolve().parents[1]
sys.path.insert(0,str(root/'sdk/gen/python'))
from ownsolid_sdk.identity.client import AuthenticatedClient
from ownsolid_sdk.identity.api.administration import list_audit_events
from ownsolid_sdk.identity.models.list_audit_events_x_ownsolid_audit_actor import ListAuditEventsXOwnsolidAuditActor

PATH='/api/v1/admin/audit-events'
REQUEST='33333333-3333-4333-8333-333333333333'
PROBLEM={'type':'about:blank','title':'STORAGE_COMMIT_OUTCOME_UNKNOWN','status':409,'code':'STORAGE_COMMIT_OUTCOME_UNKNOWN','request_id':REQUEST}
PARAMS={'category':'audit','cursor':'opaque+/=%','limit':2}

def reply(request,status=409):
    body=PROBLEM if status==409 else {'data':{'items':[],'next_cursor':None}}
    return httpx.Response(status,json=body,headers={'X-Request-ID':REQUEST,'Content-Type':'application/problem+json' if status==409 else 'application/json'})

class AuditReadWireTests(unittest.TestCase):
    def client(self,handler):
        transport=httpx.Client(base_url='https://admin.example.invalid',transport=httpx.MockTransport(handler),trust_env=False,follow_redirects=False)
        self.addCleanup(transport.close)
        return AuthenticatedClient(base_url='https://admin.example.invalid',token='').set_httpx_client(transport)
    def check_request(self,request,assistant=False):
        self.assertEqual(request.method,'GET');self.assertEqual(request.url.path,PATH)
        self.assertEqual(dict(request.url.params),{'category':'audit','cursor':'opaque+/=%','limit':'2'})
        self.assertEqual(request.content,b'');self.assertNotIn('Idempotency-Key',request.headers)
        self.assertEqual(request.headers.get('X-Ownsolid-Audit-Actor'),'true' if assistant else None)
    def check_problem(self,response):
        self.assertEqual(response.status_code,409)
        self.assertEqual(response.parsed.code.value,'STORAGE_COMMIT_OUTCOME_UNKNOWN')
        self.assertEqual(str(response.parsed.request_id),REQUEST)
        self.assertNotIn('data',response.parsed.to_dict())
    def test_409_is_returned_once_for_each_representation(self):
        for assistant in (False,True):
            with self.subTest(assistant=assistant):
                calls=[]
                def handler(request):
                    self.check_request(request,assistant);calls.append(request);return reply(request)
                client=self.client(handler)
                header={'x_ownsolid_audit_actor':ListAuditEventsXOwnsolidAuditActor.TRUE} if assistant else {}
                self.check_problem(list_audit_events.sync_detailed(client=client,**PARAMS,**header))
                self.assertEqual(len(calls),1)
    def test_explicit_retry_is_a_new_read_with_the_same_selection(self):
        calls=[]
        def handler(request):
            self.check_request(request);calls.append(request);return reply(request,409 if len(calls)==1 else 200)
        client=self.client(handler)
        self.check_problem(list_audit_events.sync_detailed(client=client,**PARAMS));self.assertEqual(len(calls),1)
        second=list_audit_events.sync_detailed(client=client,**PARAMS)
        self.assertEqual(len(calls),2);self.assertEqual(second.parsed.data.items,[])
        self.assertIsNone(second.parsed.data.next_cursor)
    def test_read_error_is_not_caught_and_retried(self):
        calls=[]
        def handler(request):
            self.check_request(request);calls.append(request);raise httpx.ReadError('controlled lost reply',request=request)
        client=self.client(handler)
        with self.assertRaises(httpx.ReadError):list_audit_events.sync_detailed(client=client,**PARAMS)
        self.assertEqual(len(calls),1)
    def test_permission_problem_is_not_a_success_or_a_retry(self):
        calls=[]
        def handler(request):
            calls.append(request);body=dict(PROBLEM,title='ACCESS_DENIED',status=403,code='ACCESS_DENIED')
            return httpx.Response(403,json=body)
        response=list_audit_events.sync_detailed(client=self.client(handler),**PARAMS)
        self.assertEqual(response.status_code,403);self.assertEqual(response.parsed.code.value,'ACCESS_DENIED')
        self.assertEqual(len(calls),1);self.assertNotIn('data',response.parsed.to_dict())
    def test_async_409_is_returned_once(self):
        async def run():
            calls=[]
            def handler(request):self.check_request(request,True);calls.append(request);return reply(request)
            async with httpx.AsyncClient(base_url='https://admin.example.invalid',transport=httpx.MockTransport(handler),trust_env=False,follow_redirects=False) as transport:
                client=AuthenticatedClient(base_url='https://admin.example.invalid',token='').set_async_httpx_client(transport)
                self.check_problem(await list_audit_events.asyncio_detailed(client=client,**PARAMS,x_ownsolid_audit_actor=ListAuditEventsXOwnsolidAuditActor.TRUE))
                self.assertEqual(len(calls),1)
        asyncio.run(run())
    def test_async_explicit_retry_is_a_new_read(self):
        async def run():
            calls=[]
            def handler(request):self.check_request(request);calls.append(request);return reply(request,409 if len(calls)==1 else 200)
            async with httpx.AsyncClient(base_url='https://admin.example.invalid',transport=httpx.MockTransport(handler),trust_env=False,follow_redirects=False) as transport:
                client=AuthenticatedClient(base_url='https://admin.example.invalid',token='').set_async_httpx_client(transport)
                self.check_problem(await list_audit_events.asyncio_detailed(client=client,**PARAMS));self.assertEqual(len(calls),1)
                second=await list_audit_events.asyncio_detailed(client=client,**PARAMS)
                self.assertEqual(second.status_code,200);self.assertEqual(second.parsed.data.items,[]);self.assertEqual(len(calls),2)
        asyncio.run(run())

if __name__=='__main__':unittest.main(verbosity=2)
