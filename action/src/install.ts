import * as toolCache from '@actions/tool-cache'
import { createHash } from 'node:crypto'
import { createReadStream } from 'node:fs'
import { chmod, readFile } from 'node:fs/promises'
import path from 'node:path'
import { valid as validSemver } from 'semver'
import { type PublishedRelease, resolvePublishedRelease } from './github.js'

const toolName = 'simplestreams-s3'
const checksumsAssetName = 'checksums.txt'

export interface InstallOptions {
  requestedVersion: string
  githubToken?: string
  platform?: string
  architecture?: string
}

export interface InstalledCli {
  version: string
  path: string
  cacheHit: boolean
}

export interface InstallerDependencies {
  resolveRelease(
    requestedVersion: string,
    githubToken?: string
  ): Promise<PublishedRelease>
  findTool(tool: string, version: string, architecture: string): string
  downloadTool(url: string, githubToken?: string): Promise<string>
  readText(filename: string): Promise<string>
  sha256(filename: string): Promise<string>
  cacheFile(
    source: string,
    target: string,
    tool: string,
    version: string,
    architecture: string
  ): Promise<string>
  makeExecutable(filename: string): Promise<void>
}

const defaultDependencies: InstallerDependencies = {
  resolveRelease: resolvePublishedRelease,
  findTool: toolCache.find,
  downloadTool: async (url, githubToken) =>
    await toolCache.downloadTool(
      url,
      undefined,
      githubToken ? `Bearer ${githubToken}` : undefined
    ),
  readText: async (filename) => await readFile(filename, 'utf8'),
  sha256: sha256File,
  cacheFile: toolCache.cacheFile,
  makeExecutable: async (filename) => await chmod(filename, 0o755)
}

/** Normalize the public version input to latest or a concrete SemVer. */
export function normalizeRequestedVersion(input: string): string {
  const trimmed = input.trim()
  if (trimmed.toLowerCase() === 'latest') return 'latest'

  const normalized = validSemver(trimmed)
  if (!normalized) {
    throw new Error(
      `Invalid CLI version ${JSON.stringify(input)}; expected latest, X.Y.Z, or vX.Y.Z`
    )
  }
  return normalized
}

/** Select the release asset published for one runner platform. */
export function releaseAssetName(
  version: string,
  platform: string,
  architecture: string
): string {
  const releaseOs = new Map([
    ['linux', 'linux'],
    ['darwin', 'darwin']
  ]).get(platform)
  if (!releaseOs) {
    throw new Error(
      `Unsupported runner platform ${platform}; simplestreams-s3 releases support linux and darwin`
    )
  }

  const releaseArchitecture = new Map([
    ['x64', 'amd64'],
    ['arm64', 'arm64']
  ]).get(architecture)
  if (!releaseArchitecture) {
    throw new Error(
      `Unsupported runner architecture ${architecture}; simplestreams-s3 releases support x64 and arm64`
    )
  }

  return `${toolName}_${version}_${releaseOs}_${releaseArchitecture}`
}

/** Return the checksum for an exact asset name. */
export function checksumForAsset(checksums: string, assetName: string): string {
  let found = ''
  for (const line of checksums.split(/\r?\n/u)) {
    if (!line.trim()) continue
    const match = /^([0-9a-f]{64})[\t ]+\*?(.+)$/iu.exec(line)
    if (!match) {
      throw new Error('checksums.txt contains an invalid entry')
    }
    if (match[2] !== assetName) continue
    if (found)
      throw new Error(`checksums.txt contains duplicate ${assetName} entries`)
    found = match[1].toLowerCase()
  }
  if (!found) throw new Error(`checksums.txt does not contain ${assetName}`)
  return found
}

/** Install one verified CLI release, reusing the runner tool cache when possible. */
export async function installCli(
  options: InstallOptions,
  dependencies: InstallerDependencies = defaultDependencies
): Promise<InstalledCli> {
  const requested = normalizeRequestedVersion(options.requestedVersion)
  const platform = options.platform ?? process.platform
  const architecture = options.architecture ?? process.arch
  releaseAssetName(requested, platform, architecture)

  if (requested !== 'latest') {
    const cached = cachedCli(dependencies, requested, architecture)
    if (cached) return cached
  }

  const release = await dependencies.resolveRelease(
    requested,
    options.githubToken
  )
  const version = normalizeRequestedVersion(release.tag)
  if (version === 'latest') {
    throw new Error(`GitHub release returned invalid tag ${release.tag}`)
  }
  if (requested !== 'latest' && version !== requested) {
    throw new Error(
      `GitHub release tag ${release.tag} does not match requested version ${requested}`
    )
  }

  const cached = cachedCli(dependencies, version, architecture)
  if (cached) return cached

  const binaryName = releaseAssetName(version, platform, architecture)
  const binaryAsset = requireAsset(release, binaryName)
  const checksumsAsset = requireAsset(release, checksumsAssetName)
  const [downloadedBinary, downloadedChecksums] = await Promise.all([
    dependencies.downloadTool(binaryAsset.downloadUrl, options.githubToken),
    dependencies.downloadTool(checksumsAsset.downloadUrl, options.githubToken)
  ])

  const expectedChecksum = checksumForAsset(
    await dependencies.readText(downloadedChecksums),
    binaryName
  )
  const actualChecksum = (
    await dependencies.sha256(downloadedBinary)
  ).toLowerCase()
  if (actualChecksum !== expectedChecksum) {
    throw new Error(
      `Checksum mismatch for ${binaryName}: expected ${expectedChecksum}, got ${actualChecksum}`
    )
  }

  const cachedDirectory = await dependencies.cacheFile(
    downloadedBinary,
    toolName,
    toolName,
    version,
    architecture
  )
  const cliPath = path.join(cachedDirectory, toolName)
  await dependencies.makeExecutable(cliPath)
  return { version, path: cliPath, cacheHit: false }
}

function cachedCli(
  dependencies: InstallerDependencies,
  version: string,
  architecture: string
): InstalledCli | undefined {
  const directory = dependencies.findTool(toolName, version, architecture)
  if (!directory) return undefined
  return {
    version,
    path: path.join(directory, toolName),
    cacheHit: true
  }
}

function requireAsset(release: PublishedRelease, name: string) {
  const asset = release.assets.find((candidate) => candidate.name === name)
  if (!asset)
    throw new Error(`GitHub release ${release.tag} is missing asset ${name}`)
  return asset
}

async function sha256File(filename: string): Promise<string> {
  const hash = createHash('sha256')
  const input = createReadStream(filename)
  await new Promise<void>((resolve, reject) => {
    input.on('data', (chunk) => hash.update(chunk))
    input.on('error', reject)
    input.on('end', resolve)
  })
  return hash.digest('hex')
}
