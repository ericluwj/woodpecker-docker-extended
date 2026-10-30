import importlib.util
import json
import os
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location('release', Path(__file__).with_name('release.py'))
release = importlib.util.module_from_spec(spec)
spec.loader.exec_module(release)

SOURCE = 'require (\n go.woodpecker-ci.org/woodpecker/v3 v3.20.0\n)\n'
DIGEST = 'sha256:' + 'a' * 64


class ReleaseTests(unittest.TestCase):
    def test_version_comes_from_pinned_dependency(self):
        self.assertEqual(release.upstream_version(SOURCE), '3.20.0')
        with self.assertRaises(ValueError):
            release.upstream_version(SOURCE.replace('v3.20.0', 'v3.20.0-rc.1'))

    def test_first_revision_and_version_upgrade(self):
        self.assertEqual(release.next_release('3.20.0', []), '3.20.0-1')
        self.assertEqual(release.next_release('3.21.0', ['3.20.0-99']), '3.21.0-1')

    def test_numeric_sequence_ignores_aliases_legacy_and_other_versions(self):
        tags = ['3.20.0-2', '3.20.0-10', '3.20.0-latest', 'latest',
                'sha-abc', '3.21.0-99', '3.20.0-01']
        self.assertEqual(release.next_release('3.20.0', tags), '3.20.0-11')

    def test_reservation_includes_all_tag_pages_and_consumes_failed_revision(self):
        pages = [[{'name': '3.20.0-2'}], [{'name': '3.20.0-10'}]]
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'outputs'
            with patch.object(release, 'run', return_value=json.dumps(pages)), patch.object(release, 'api') as api:
                self.assertEqual(release.reserve('owner/repo', 'commit', SOURCE, output), '3.20.0-11')
                api.assert_called_once_with('owner/repo', 'git/refs', 'POST', ref='refs/tags/3.20.0-11', sha='commit')
            self.assertEqual(output.read_text(), 'release=3.20.0-11\nalias=3.20.0-latest\n')

    def test_reservation_collision_aborts_without_outputs(self):
        with tempfile.TemporaryDirectory() as directory:
            output = Path(directory) / 'outputs'
            error = subprocess.CalledProcessError(1, 'gh', stderr='HTTP 422')
            with patch.object(release, 'run', return_value='[[]]'), patch.object(release, 'api', side_effect=error):
                with self.assertRaises(subprocess.CalledProcessError):
                    release.reserve('owner/repo', 'commit', SOURCE, output)
            self.assertFalse(output.exists())

    def test_promote_creates_release_before_moving_only_alias(self):
        events = []
        def api(repository, path, method='GET', **fields):
            events.append(('api', path, method, fields))
            return {'object': {'sha': 'commit'}}
        def run(*args):
            events.append(args)
            return ''
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {'GITHUB_STEP_SUMMARY': str(Path(directory) / 'summary')}):
            with patch.object(release, 'api', side_effect=api), patch.object(release, 'run', side_effect=run):
                release.promote('owner/repo', 'commit', '3.20.0-2', '3.20.0-latest', DIGEST, 'owner')
        self.assertEqual(events[0][1], 'git/ref/tags/3.20.0-2')
        self.assertEqual(events[1][:4], ('gh', 'release', 'create', '3.20.0-2'))
        self.assertIn('--verify-tag', events[1])
        self.assertEqual(events[2][-2:], ('docker.io/owner/woodpecker-docker-extended:3.20.0-latest', f'docker.io/owner/woodpecker-docker-extended@{DIGEST}'))
        self.assertEqual(events[3][-1], f'ghcr.io/owner/repo@{DIGEST}')
        self.assertEqual(events[-1], ('api', 'git/refs/tags/3.20.0-latest', 'PATCH', {'sha': 'commit', 'force': True}))

    def test_first_alias_is_created_only_on_404(self):
        error = subprocess.CalledProcessError(1, 'gh', stderr='gh: Not Found (HTTP 404)')
        with tempfile.TemporaryDirectory() as directory, patch.dict(os.environ, {'GITHUB_STEP_SUMMARY': str(Path(directory) / 'summary')}):
            with patch.object(release, 'run'), patch.object(release, 'api', side_effect=[{'object': {'sha': 'commit'}}, error, {}]) as api:
                release.promote('owner/repo', 'commit', '3.20.0-1', '3.20.0-latest', DIGEST, 'owner')
                self.assertEqual(api.call_args.args, ('owner/repo', 'git/refs', 'POST'))
                self.assertEqual(api.call_args.kwargs['ref'], 'refs/tags/3.20.0-latest')

    def test_wrong_commit_never_creates_release_or_promotes(self):
        with patch.object(release, 'api', return_value={'object': {'sha': 'other'}}), patch.object(release, 'run') as run:
            with self.assertRaises(ValueError):
                release.promote('owner/repo', 'commit', '3.20.0-1', '3.20.0-latest', DIGEST, 'owner')
            run.assert_not_called()

    def test_release_or_registry_failure_does_not_update_git_alias(self):
        for fail_at in (1, 2, 3):
            calls = []
            def run(*args):
                calls.append(args)
                if len(calls) == fail_at:
                    raise subprocess.CalledProcessError(1, args)
            with self.subTest(fail_at=fail_at), patch.object(release, 'run', side_effect=run), patch.object(release, 'api', return_value={'object': {'sha': 'commit'}}) as api:
                with self.assertRaises(subprocess.CalledProcessError):
                    release.promote('owner/repo', 'commit', '3.20.0-1', '3.20.0-latest', DIGEST, 'owner')
                self.assertEqual(api.call_count, 1)

    def test_alias_lookup_permission_failure_is_not_treated_as_missing(self):
        error = subprocess.CalledProcessError(1, 'gh', stderr='gh: Forbidden (HTTP 403)')
        with patch.object(release, 'run'), patch.object(release, 'api', side_effect=[{'object': {'sha': 'commit'}}, error]) as api:
            with self.assertRaises(subprocess.CalledProcessError):
                release.promote('owner/repo', 'commit', '3.20.0-1', '3.20.0-latest', DIGEST, 'owner')
            self.assertEqual(api.call_count, 2)

    def test_reject_invalid_alias_and_digest_before_remote_calls(self):
        for alias, digest in [('3.21.0-latest', DIGEST), ('latest', DIGEST), ('3.20.0-latest', 'not-a-digest')]:
            with self.subTest(alias=alias, digest=digest), patch.object(release, 'api') as api:
                with self.assertRaises(ValueError):
                    release.promote('owner/repo', 'commit', '3.20.0-1', alias, digest, 'owner')
                api.assert_not_called()


if __name__ == '__main__':
    unittest.main()
