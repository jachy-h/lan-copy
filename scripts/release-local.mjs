#!/usr/bin/env node
// 本地部署目标；请求地址由此拼接为 /api/app 和 /api/deploy。
export const DEPLOY_URL = 'http://192.168.2.88:8080/';

import { spawnSync } from 'node:child_process';
import { existsSync } from 'node:fs';
import { dirname, resolve } from 'node:path';
import { fileURLToPath, pathToFileURL } from 'node:url';
import { parseArgs } from 'node:util';

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..');

export function release(args = process.argv.slice(2), env = process.env, run = spawnSync) {
  const { values } = parseArgs({ args, options: {
    url: { type: 'string', default: DEPLOY_URL },
    package: { type: 'string' },
    version: { type: 'string' },
    'skip-build': { type: 'boolean', default: false },
    'dry-run': { type: 'boolean', default: false },
    help: { type: 'boolean', short: 'h' },
  }});
  if (values.help) {
    console.log('node scripts/release-local.mjs --package <目标平台压缩包> [--version x.y.z] [--skip-build] [--url URL] [--dry-run]');
    console.log('默认先 make build，再上传并确认重启。使用 VERSION 中的版本，不递增、不提交、不推送 Git。');
    return;
  }
  if (!values.package) throw new Error('请用 --package 指定目标平台的发布包');
  if (values.version && !/^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/.test(values.version)) {
    throw new Error('--version 必须为 MAJOR.MINOR.PATCH');
  }
  if (values['skip-build'] && values.version) throw new Error('--skip-build 不能与 --version 同时使用');
  const target = new URL(values.url);
  if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password || target.search || target.hash) {
    throw new Error('部署地址必须为不含凭据、查询或片段的 http(s) 地址');
  }
  const packagePath = resolve(ROOT, values.package);
  if (!values['dry-run'] && !env.LAN_COPY_DEPLOY_TOKEN) throw new Error('请先设置 LAN_COPY_DEPLOY_TOKEN');
  const commands = [];
  if (!values['skip-build']) commands.push(['make', ['build', ...(values.version ? [`VERSION=${values.version}`] : [])]]);
  commands.push(['python3', [resolve(ROOT, 'scripts/deploy.py'), '--url', target.href, '--package', packagePath]]);
  console.log(`本地发布目标：${target.href}`);
  for (const [command, commandArgs] of commands) {
    if (values['dry-run']) {
      console.log(JSON.stringify([command, ...commandArgs]));
      continue;
    }
    if (command === 'python3' && !existsSync(packagePath)) throw new Error(`发布包不存在：${packagePath}`);
    const result = run(command, commandArgs, { cwd: ROOT, env, stdio: 'inherit', shell: false });
    if (result.error) throw result.error;
    if (result.status !== 0) throw new Error(`${command} 执行失败（${result.signal || result.status}），已停止本地发布`);
  }
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try { release(); } catch (error) {
    console.error(`本地发布失败：${error.message}`);
    process.exitCode = 1;
  }
}
