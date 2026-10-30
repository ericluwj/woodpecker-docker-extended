#!/usr/bin/env python3
"""Reserve numbered releases and promote per-Woodpecker-version aliases."""
import argparse
import json
import os
from pathlib import Path
import re
import subprocess
import tempfile


def run(*args):
    return subprocess.run(args, check=True, text=True, capture_output=True).stdout.strip()


def api(repository, path, method='GET', **fields):
    args = ['gh', 'api', '--method', method, f'repos/{repository}/{path}']
    for key, value in fields.items():
        args.extend(['-F', f'{key}={str(value).lower() if isinstance(value, bool) else value}'])
    return json.loads(run(*args))


def upstream_version(source):
    match = re.search(r'^\s*go\.woodpecker-ci\.org/woodpecker/v3 v(\d+\.\d+\.\d+)\s*$', source, re.M)
    if not match:
        raise ValueError('go.mod must pin a stable Woodpecker v3 version')
    return match[1]


def next_release(version, tags):
    pattern = re.compile(re.escape(version) + r'-([1-9][0-9]*)$')
    revisions = [int(match[1]) for tag in tags if (match := pattern.fullmatch(tag))]
    return f'{version}-{max(revisions, default=0) + 1}'


def reserve(repository, commit, source, output):
    version = upstream_version(source)
    pages = json.loads(run('gh', 'api', '--paginate', '--slurp',
                           f'repos/{repository}/tags?per_page=100'))
    release = next_release(version, [tag['name'] for page in pages for tag in page])
    # Creating an existing ref fails. Never move a numbered tag, even on retry.
    api(repository, 'git/refs', 'POST', ref=f'refs/tags/{release}', sha=commit)
    with open(output, 'a', encoding='utf-8') as stream:
        stream.write(f'release={release}\nalias={version}-latest\n')
    return release


def publish_alias_release(repository, release, alias, notes):
    try:
        existing = api(repository, f'releases/tags/{alias}')
    except subprocess.CalledProcessError as error:
        if '(HTTP 404)' not in (error.stderr or ''):
            raise
        action = 'create'
    else:
        if existing.get('immutable'):
            raise ValueError('the alias release is immutable and cannot move')
        action = 'edit'
    body = (f'Moving release alias. Current numbered release: '
            f'[{release}](https://github.com/{repository}/releases/tag/{release}).\n\n' + notes)
    with tempfile.TemporaryDirectory() as directory:
        notes_file = Path(directory) / 'alias-notes.md'
        notes_file.write_text(body)
        args = ['gh', 'release', action, alias, '--repo', repository,
                '--title', alias, '--notes-file', str(notes_file), '--latest']
        if action == 'create':
            args.append('--verify-tag')
        run(*args)


def promote(repository, commit, release, alias, digest, username):
    match = re.fullmatch(r'(\d+\.\d+\.\d+)-[1-9][0-9]*', release)
    if not match or alias != f'{match[1]}-latest':
        raise ValueError('alias must match the numbered release Woodpecker version')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', digest):
        raise ValueError('expected an image index digest')
    if not re.fullmatch(r'[a-z0-9][a-z0-9_-]*', username):
        raise ValueError('invalid Docker Hub username')
    # Verify the reservation still points to the verified source before release.
    ref = api(repository, f'git/ref/tags/{release}')
    if ref['object']['sha'] != commit:
        raise ValueError('numbered tag does not point to the verified commit')
    images = [f'docker.io/{username}/woodpecker-docker-extended',
              f'ghcr.io/{repository.lower()}']
    notes = '\n'.join([f'Source commit: `{commit}`', '', 'Multi-platform Linux AMD64/ARM64 images:',
                       *[f'- `{image}:{release}` (`{image}@{digest}`)' for image in images]]) + '\n'
    with tempfile.TemporaryDirectory() as directory:
        notes_file = Path(directory) / 'release-notes.md'
        notes_file.write_text(notes)
        run('gh', 'release', 'create', release, '--repo', repository, '--verify-tag',
            '--title', release, '--notes-file', str(notes_file), '--latest=false')
    # Only promote after both immutable image tags and the numbered release exist.
    for image in images:
        run('docker', 'buildx', 'imagetools', 'create', '--tag', f'{image}:{alias}', f'{image}@{digest}')
    # The alias release stays mutable; native release immutability must be disabled.
    try:
        api(repository, f'git/ref/tags/{alias}')
    except subprocess.CalledProcessError as error:
        if '(HTTP 404)' not in (error.stderr or ''):
            raise
        api(repository, 'git/refs', 'POST', ref=f'refs/tags/{alias}', sha=commit)
    else:
        api(repository, f'git/refs/tags/{alias}', 'PATCH', sha=commit, force=True)
    publish_alias_release(repository, release, alias, notes)
    with open(os.environ['GITHUB_STEP_SUMMARY'], 'a', encoding='utf-8') as stream:
        stream.write(f'## Release {release}\n\n{notes}\nMoving alias: `{alias}`\n')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('command', choices=['reserve', 'promote'])
    args = parser.parse_args()
    repository, commit = os.environ['GITHUB_REPOSITORY'], os.environ['GITHUB_SHA']
    if args.command == 'reserve':
        reserve(repository, commit, Path('go.mod').read_text(), os.environ['GITHUB_OUTPUT'])
    else:
        promote(repository, commit, os.environ['RELEASE'], os.environ['ALIAS'],
                os.environ['IMAGE_DIGEST'], os.environ['DOCKERHUB_USERNAME'])


if __name__ == '__main__':
    main()
