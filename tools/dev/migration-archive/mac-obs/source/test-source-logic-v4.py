import ast
import errno
from pathlib import Path
from types import SimpleNamespace
import unittest
from unittest.mock import Mock

BASE = Path('/private/tmp/obs110-main-integration-pb1qgf_t')
TREE = ast.parse((BASE/'check-source-logic-v4.py').read_text())
MAIN = next(n for n in TREE.body if isinstance(n, ast.Try))

def run_nodes(nodes, values):
    module = ast.fix_missing_locations(ast.Module(body=nodes, type_ignores=[]))
    exec(compile(module, '<pure-wrapper-parameters>', 'exec', optimize=1), values)

class PureEntryBoundary(unittest.TestCase):
    def test_optimized_python_is_explicitly_refused(self):
        guard = next(n for n in TREE.body if isinstance(n, ast.If))
        with self.assertRaisesRegex(SystemExit, 'OPTIMIZED_PYTHON_FORBIDDEN'):
            run_nodes([guard], {'sys': SimpleNamespace(flags=SimpleNamespace(optimize=1))})
        run_nodes([guard], {'sys': SimpleNamespace(flags=SimpleNamespace(optimize=0))})

    def test_only_the_one_flock_refusal_becomes_admission_busy(self):
        definitions = [n for n in TREE.body if isinstance(n, (ast.ClassDef, ast.FunctionDef)) and n.name in ('AdmissionBusy','acquire_once')]
        for error, category in [(None,None), (BlockingIOError(errno.EWOULDBLOCK,'busy'),'AdmissionBusy'), (OSError(errno.EINVAL,'invalid'),OSError)]:
            flock = Mock(side_effect=error)
            values={'fcntl':SimpleNamespace(flock=flock, LOCK_EX=2, LOCK_NB=4)}
            run_nodes(definitions, values)
            if category is None:
                values['acquire_once'](23)
            else:
                expected = values[category] if isinstance(category,str) else category
                with self.assertRaises(expected): values['acquire_once'](23)
            flock.assert_called_once_with(23,6)
        self.assertEqual([h.type.id for h in MAIN.handlers],['AdmissionBusy','Exception'])
        self.assertFalse(issubclass(BlockingIOError,values['AdmissionBusy']))

    def environment(self):
        assignment = next(n for n in MAIN.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='env' for t in n.targets))
        update = next(n for n in MAIN.body if isinstance(n,ast.Expr) and isinstance(n.value,ast.Call) and isinstance(n.value.func,ast.Attribute) and isinstance(n.value.func.value,ast.Name) and n.value.func.value.id=='env' and n.value.func.attr=='update')
        values={'os':SimpleNamespace(environ={'GOFLAGS':'-tags=repository_integration,monitoring_integration','GOENV':'/foreign/goenv','GOOS':'linux','GOARCH':'amd64','GOTOOLCHAIN':'auto','CGO_ENABLED':'0','CGO_CFLAGS':'foreign','CC':'foreign','PROJECT_ONE_REPOSITORY_ENGINE':'postgresql','OWNSOLID_TEST_VICTORIA_METRICS':'foreign','PATH':'test-path'}),'ROOT':BASE/'producer-source-native-import'}
        run_nodes([assignment,update],values)
        return values['env']

    def test_inherited_go_and_native_inputs_are_removed(self):
        env=self.environment()
        self.assertEqual(env['GOFLAGS'],'')
        self.assertEqual(env['GOENV'],'off')
        self.assertEqual(env['GOOS'],'darwin')
        self.assertEqual(env['GOARCH'],'arm64')
        self.assertEqual(env['CGO_ENABLED'],'1')
        self.assertEqual(env['GOTOOLCHAIN'],'local')
        self.assertEqual(env['GOPROXY'],'off')
        self.assertEqual(env['GOSUMDB'],'off')
        self.assertEqual(env['GOMAXPROCS'],'2')
        self.assertFalse(any(k.startswith(('PROJECT_ONE_','OWNSOLID_TEST_')) for k in env))
        self.assertNotIn('CC',env)
        self.assertNotIn('CGO_CFLAGS',env)

    def test_native_artifact_is_only_a_compile_destination(self):
        loop=next(n for n in MAIN.body if isinstance(n,ast.For))
        statements=loop.body[:3]
        self.assertIsInstance(statements[2],ast.If)
        for roots in [18,None]:
            values={'env':self.environment(),'GO':'/pinned/go','roots':roots,'binary':Path('/owned/result.test'),'selection':['./internal/modules/monitoring']}
            run_nodes(statements,values)
            cmd,env=values['cmd'],values['stage_env']
            self.assertEqual(cmd[0],'/pinned/go')
            self.assertNotEqual(cmd[0],str(values['binary']))
            if roots is None:
                self.assertIn('-c',cmd)
                self.assertEqual(cmd[cmd.index('-o')+1],str(values['binary']))
                self.assertEqual((env['GOOS'],env['GOARCH'],env['CGO_ENABLED']),('linux','amd64','0'))
            else:
                self.assertNotIn('-c',cmd)
                self.assertFalse(any(x.startswith('-tags') for x in cmd))
                self.assertEqual(env['GOOS'],'darwin')

    def test_wrapper_config_and_source_pins_match(self):
        import hashlib,json,subprocess
        config_bytes=(BASE/'source-logic-v4.json').read_bytes()
        config=json.loads(config_bytes)
        literal=next(n.value.value for n in TREE.body if isinstance(n,ast.Assign) and any(isinstance(t,ast.Name) and t.id=='CONFIG_SHA256' for t in n.targets))
        self.assertEqual(hashlib.sha256(config_bytes).hexdigest(),literal)
        root=Path(config['root'])
        self.assertEqual(subprocess.check_output(['git','rev-parse','HEAD'],cwd=root,text=True).strip(),config['source'])
        self.assertEqual(subprocess.check_output(['git','rev-parse','HEAD^{tree}'],cwd=root,text=True).strip(),config['tree'])
        self.assertEqual(subprocess.check_output(['git','status','--porcelain'],cwd=root,text=True),'')

if __name__=='__main__': unittest.main(verbosity=2)
