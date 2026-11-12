#!/usr/bin/env node
// Build and deploy to the configured Lan Copy target.
import { spawnSync } from 'node:child_process'
import { readFileSync } from 'node:fs'
import { dirname, resolve } from 'node:path'
import { fileURLToPath, pathToFileURL } from 'node:url'
import { parseArgs } from 'node:util'
import { DEPLOY_URL, release } from './release-local.mjs'

const ROOT = resolve(dirname(fileURLToPath(import.meta.url)), '..')
const TOKEN_FILE = resolve(ROOT, '.config/deploy.token')
const PACKAGES = {
  'windows/amd64': 'release/Lan-Copy-Windows-amd64.zip',
  'darwin/arm64': 'release/Lan-Copy-macOS-Apple-Silicon.tar.gz',
}

export function packageFor(info) {
  const key = `${info?.os || ''}/${info?.arch || ''}`
  const packagePath = PACKAGES[key]
  if (!packagePath) throw new Error(`目标平台 ${key || '未知'} 没有对应的发布包`)
  return packagePath
}

export async function platformOf(url, fetchImpl = fetch) {
  const target = new URL(url)
  if (!['http:', 'https:'].includes(target.protocol) || target.username || target.password || target.search || target.hash) {
    throw new Error('部署地址必须为不含凭据、查询或片段的 http(s) 地址')
  }
  let response
  try {
    response = await fetchImpl(new URL('api/app', target), {
      headers: { 'Cache-Control': 'no-cache' },
      signal: AbortSignal.timeout(5000),
    })
  } catch (error) {
    throw new Error(`无法连接部署目标：${error instanceof Error ? error.message : error}`)
  }
  if (!response.ok) throw new Error(`无法读取目标平台（HTTP ${response.status}）`)
  const info = await response.json()
  if (!info || typeof info.os !== 'string' || typeof info.arch !== 'string') throw new Error('目标 /api/app 返回的平台信息无效')
  return info
}

export function tokenFrom(env = process.env, tokenFile = TOKEN_FILE) {
  if (env.LAN_COPY_DEPLOY_TOKEN) return env.LAN_COPY_DEPLOY_TOKEN
  try { return readFileSync(tokenFile, 'utf8').trim() } catch { return '' }
}

export async function deployTarget(args = process.argv.slice(2), env = process.env, run = spawnSync, fetchImpl = fetch) {
  const { values } = parseArgs({ args, options: {
    url: { type: 'string', default: DEPLOY_URL },
    package: { type: 'string' },
    version: { type: 'string' },
    'skip-build': { type: 'boolean', default: false },
    'dry-run': { type: 'boolean', default: false },
    help: { type: 'boolean', short: 'h' },
  } })
  if (values.help) {
    console.log('make deploy                    # 构建并部署到默认目标')
    console.log('make deploy DEPLOY_URL=…       # 部署到其他目标')
    console.log('make deploy DEPLOY_PACKAGE=…   # 指定目标平台发布包')
    console.log('make deploy VERSION=x.y.z      # 使用指定版本构建')
    console.log('node scripts/deploy-target.mjs [--url URL] [--package 包] [--version x.y.z] [--skip-build] [--dry-run]')
    return
  }
  const packagePath = values.package || packageFor(await platformOf(values.url, fetchImpl))
  const forwarded = ['--package', packagePath, '--url', values.url]
  if (values.version) forwarded.push('--version', values.version)
  if (values['skip-build']) forwarded.push('--skip-build')
  if (values['dry-run']) forwarded.push('--dry-run')
  const token = tokenFrom(env)
  if (!values['dry-run'] && !token) throw new Error('未找到部署令牌：请执行 make deploytoken，或在 .config/deploy.token 配置')
  release(forwarded, token && !env.LAN_COPY_DEPLOY_TOKEN ? { ...env, LAN_COPY_DEPLOY_TOKEN: token } : env, run)
}

if (process.argv[1] && import.meta.url === pathToFileURL(resolve(process.argv[1])).href) {
  try { await deployTarget() } catch (error) {
    console.error(`部署失败：${error.message}`)
    process.exitCode = 1
  }
}
