import test from 'node:test';
import assert from 'node:assert/strict';
import { release, DEPLOY_URL } from './release-local.mjs';

test('build failure stops deployment', () => {
  const calls = [];
  assert.throws(() => release(['--package', 'release/test.zip'], { LAN_COPY_DEPLOY_TOKEN: 'secret' }, (...args) => {
    calls.push(args);
    return { status: 1 };
  }), /make 执行失败/);
  assert.equal(calls.length, 1);
  assert.deepEqual(calls[0][1], ['build']);
  assert.equal(calls[0][2].shell, false);
});

test('requires token and validates version before build', () => {
  assert.throws(() => release(['--package', 'test.zip'], {}), /LAN_COPY_DEPLOY_TOKEN/);
  assert.throws(() => release(['--package', 'test.zip', '--version', 'bad'], {}), /MAJOR/);
  assert.throws(() => release(['--package', 'test.zip', '--skip-build', '--version', '1.2.3'], {}), /不能/);
});

test('dry run uses configured address without executing anything', () => {
  assert.equal(DEPLOY_URL, 'http://192.168.2.88:8080/');
  release(['--package', 'release/test.zip', '--dry-run'], {}, () => assert.fail('dry run executed a command'));
});

test('deployment-only passes exact arguments without shell interpolation', () => {
  const calls = [];
  release(['--package', 'scripts/deploy.py', '--skip-build', '--url', 'https://host.test/base'], { LAN_COPY_DEPLOY_TOKEN: 'secret' }, (...args) => {
    calls.push(args);
    return { status: 0 };
  });
  assert.equal(calls.length, 1);
  assert.equal(calls[0][0], 'python3');
  assert.equal(calls[0][1][2], 'https://host.test/base');
  assert.equal(calls[0][2].env.LAN_COPY_DEPLOY_TOKEN, 'secret');
});
