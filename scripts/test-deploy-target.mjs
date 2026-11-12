import test from 'node:test';
import assert from 'node:assert/strict';
import { packageFor, platformOf, tokenFrom } from './deploy-target.mjs';

test('selects a release package from the target platform', () => {
  assert.equal(packageFor({ os: 'windows', arch: 'amd64' }), 'release/Lan-Copy-Windows-amd64.zip');
  assert.equal(packageFor({ os: 'darwin', arch: 'arm64' }), 'release/Lan-Copy-macOS-Apple-Silicon.tar.gz');
  assert.throws(() => packageFor({ os: 'linux', arch: 'amd64' }), /没有对应的发布包/);
});

test('reads platform information from the target base path', async () => {
  let requested = '';
  const info = await platformOf('http://host.test/base/', async url => {
    requested = String(url);
    return { ok: true, status: 200, json: async () => ({ os: 'windows', arch: 'amd64' }) };
  });
  assert.equal(requested, 'http://host.test/base/api/app');
  assert.deepEqual(info, { os: 'windows', arch: 'amd64' });
});

test('rejects invalid deployment addresses and responses', async () => {
  await assert.rejects(platformOf('https://user:pass@host.test/'), /地址/);
  await assert.rejects(platformOf('http://host.test/', async () => ({ ok: false, status: 503 })), /HTTP 503/);
  await assert.rejects(platformOf('http://host.test/', async () => ({ ok: true, status: 200, json: async () => ({ os: 'windows' }) })), /平台信息无效/);
});

test('uses the environment token before the local token file', () => {
  assert.equal(tokenFrom({ LAN_COPY_DEPLOY_TOKEN: 'from-env' }, '/missing'), 'from-env');
  assert.equal(tokenFrom({}, '/missing'), '');
});
