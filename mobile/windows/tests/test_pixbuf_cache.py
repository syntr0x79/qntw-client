"""Regression checks for cold-start cache generation without loader subprocesses."""
import copy
import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import Mock

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
import pixbuf_cache as cache

class LoaderCacheTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name).resolve() / 'Program Files' / 'Quantway'
        self.root.mkdir(parents=True)
        self.cache_dir = Path(self.temp.name) / 'private-cache'
        lock = self.root / 'licenses/msys2-runtime-lock.json'
        lock.parent.mkdir()
        lock.write_bytes(b'locked runtime')
        self.module = self.root / cache.LOADERS / 'test_png.dll'
        self.module.parent.mkdir(parents=True)
        self.module.write_bytes(b'locked DLL')
        self.manifest = {'schema': 1, 'runtime_lock_sha256': cache.sha(lock),
            'modules': [{'file': 'test_png.dll', 'record': '"png" 5 "gdk-pixbuf" "PNG" "LGPL"\n"image/png" ""\n"png" ""\n"PNG" "" 100'}],
            'architectures': {'arm64': {'module_sha256': {'test_png.dll': cache.sha(self.module)}}}}
        (self.root / 'pixbuf-loaders.json').write_text(json.dumps(self.manifest))
        (self.root / 'BUILD-INFO.json').write_text(json.dumps({'architecture': 'windows/arm64', 'version': 'old'}))

    def test_cold_packaged_start_never_launches_query(self):
        runner = Mock(side_effect=AssertionError('packaged startup launched a query'))
        path, source = cache.prepare_cache(self.root, self.cache_dir, runner=runner)
        self.assertEqual(source, 'bundled')
        runner.assert_not_called()
        self.assertIn(json.dumps(self.module.as_posix()), path.read_text())
        self.assertNotIn('C:/Users/', path.read_text())
        self.assertTrue(path.is_relative_to(self.cache_dir))

    def test_warm_cache_survives_go_only_version_update(self):
        first, _ = cache.prepare_cache(self.root, self.cache_dir)
        before = first.stat().st_mtime_ns
        (self.root / 'BUILD-INFO.json').write_text(json.dumps({'architecture': 'windows/arm64', 'version': 'new'}))
        second, source = cache.prepare_cache(self.root, self.cache_dir)
        self.assertEqual((first, before), (second, second.stat().st_mtime_ns))
        self.assertEqual(source, 'bundled')

    def test_developer_query_timeout_is_nonfatal_private_fallback(self):
        (self.root / 'pixbuf-loaders.json').unlink()
        runner = Mock(side_effect=subprocess.TimeoutExpired('query', 3))
        warnings = []
        path, source = cache.prepare_cache(self.root, self.cache_dir, warn=warnings.append, runner=runner)
        self.assertEqual(source, 'empty-fallback')
        self.assertEqual(runner.call_args.kwargs['timeout'], 3)
        self.assertNotIn('GDK_PIXBUF_MODULE_FILE', runner.call_args.kwargs['env'])
        self.assertEqual(runner.call_args.kwargs['env']['GDK_PIXBUF_MODULEDIR'], str(self.root / cache.LOADERS))
        self.assertEqual(len(warnings), 2)
        self.assertTrue(path.is_relative_to(self.cache_dir))
        self.assertNotIn('.dll', path.read_text())

    def test_developer_failed_query_is_nonfatal(self):
        (self.root / 'pixbuf-loaders.json').unlink()
        runner = Mock(return_value=subprocess.CompletedProcess([], 1, b'', b'not ready'))
        path, source = cache.prepare_cache(self.root, self.cache_dir, runner=runner)
        self.assertEqual(source, 'empty-fallback')
        self.assertTrue(path.exists())

    def test_queried_paths_rebased_to_owned_module_directory(self):
        (self.root / 'pixbuf-loaders.json').unlink()
        output = json.dumps('C:/another/install/test_png.dll') + '\n' + self.manifest['modules'][0]['record'] + '\n'
        runner = Mock(return_value=subprocess.CompletedProcess([], 0, output.encode(), b''))
        path, source = cache.prepare_cache(self.root, self.cache_dir, runner=runner)
        self.assertEqual(source, 'query')
        self.assertIn(json.dumps(self.module.as_posix()), path.read_text())
        self.assertNotIn('another/install', path.read_text())

    def test_packaging_rejects_wrong_module_hash(self):
        self.module.write_bytes(b'replaced DLL')
        with self.assertRaisesRegex(ValueError, 'hash mismatch'):
            cache.from_manifest(self.root, self.manifest, 'arm64', verify_hashes=True)

    def test_packaging_rejects_unlocked_runtime(self):
        (self.root / 'licenses/msys2-runtime-lock.json').write_bytes(b'another runtime')
        with self.assertRaisesRegex(ValueError, 'runtime lock'):
            cache.from_manifest(self.root, self.manifest, 'arm64')

    def test_invalid_duplicate_traversal_or_missing_module_rejected(self):
        for modules in [self.manifest['modules'] * 2,
                [{'file': '../test_png.dll', 'record': '"png"'}],
                [{'file': 'missing.dll', 'record': '"png"'}]]:
            with self.subTest(modules=modules), self.assertRaises(ValueError):
                cache.render_records(self.root, modules)

if __name__ == '__main__':
    unittest.main()
