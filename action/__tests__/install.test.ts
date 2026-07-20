import { jest } from '@jest/globals'
import path from 'node:path'
import {
  checksumForAsset,
  installCli,
  type InstallerDependencies,
  normalizeRequestedVersion,
  releaseAssetName
} from '../src/install.js'
import type { PublishedRelease } from '../src/github.js'

const checksum = 'a'.repeat(64)
const release: PublishedRelease = {
  tag: 'v0.1.0',
  prerelease: false,
  assets: [
    {
      name: 'simplestreams-s3_0.1.0_linux_amd64',
      downloadUrl: 'https://example.test/binary'
    },
    { name: 'checksums.txt', downloadUrl: 'https://example.test/checksums' }
  ]
}

function createDependencies(
  overrides: Partial<InstallerDependencies> = {}
): InstallerDependencies {
  return {
    resolveRelease: jest.fn(async () => release),
    findTool: jest.fn(() => ''),
    downloadTool: jest.fn(async (url) =>
      url.endsWith('checksums') ? '/tmp/checksums' : '/tmp/binary'
    ),
    readText: jest.fn(
      async () => `${checksum}  simplestreams-s3_0.1.0_linux_amd64\n`
    ),
    sha256: jest.fn(async () => checksum),
    cacheFile: jest.fn(async () => '/toolcache/simplestreams-s3/0.1.0/x64'),
    makeExecutable: jest.fn(async () => undefined),
    ...overrides
  }
}

describe('normalizeRequestedVersion', () => {
  it.each([
    ['latest', 'latest'],
    [' LATEST ', 'latest'],
    ['0.1.0', '0.1.0'],
    ['v1.2.3-beta.1', '1.2.3-beta.1']
  ])('normalizes %s', (input, expected) => {
    expect(normalizeRequestedVersion(input)).toBe(expected)
  })

  it('rejects non-SemVer input', () => {
    expect(() => normalizeRequestedVersion('main')).toThrow(
      'expected latest, X.Y.Z, or vX.Y.Z'
    )
  })
})

describe('releaseAssetName', () => {
  it.each([
    ['linux', 'x64', 'simplestreams-s3_1.2.3_linux_amd64'],
    ['linux', 'arm64', 'simplestreams-s3_1.2.3_linux_arm64'],
    ['darwin', 'x64', 'simplestreams-s3_1.2.3_darwin_amd64'],
    ['darwin', 'arm64', 'simplestreams-s3_1.2.3_darwin_arm64']
  ])('maps %s/%s', (platform, architecture, expected) => {
    expect(releaseAssetName('1.2.3', platform, architecture)).toBe(expected)
  })

  it('rejects unsupported operating systems and architectures', () => {
    expect(() => releaseAssetName('1.2.3', 'win32', 'x64')).toThrow(
      'Unsupported runner platform win32'
    )
    expect(() => releaseAssetName('1.2.3', 'linux', 's390x')).toThrow(
      'Unsupported runner architecture s390x'
    )
  })
})

describe('checksumForAsset', () => {
  it('selects one exact checksum entry', () => {
    expect(
      checksumForAsset(
        `${'b'.repeat(64)}  other\n${checksum} *wanted\n`,
        'wanted'
      )
    ).toBe(checksum)
  })

  it('rejects malformed, duplicate, and missing entries', () => {
    expect(() => checksumForAsset('bad line', 'wanted')).toThrow(
      'contains an invalid entry'
    )
    expect(() =>
      checksumForAsset(`${checksum}  wanted\n${checksum}  wanted\n`, 'wanted')
    ).toThrow('duplicate wanted entries')
    expect(() => checksumForAsset(`${checksum}  other\n`, 'wanted')).toThrow(
      'does not contain wanted'
    )
  })
})

describe('installCli', () => {
  it('uses a concrete cached version without a GitHub request', async () => {
    const dependencies = createDependencies({
      findTool: jest.fn(() => '/toolcache/simplestreams-s3/0.1.0/x64')
    })

    const installed = await installCli(
      {
        requestedVersion: 'v0.1.0',
        platform: 'linux',
        architecture: 'x64'
      },
      dependencies
    )

    expect(installed).toEqual({
      version: '0.1.0',
      path: path.join(
        '/toolcache/simplestreams-s3/0.1.0/x64',
        'simplestreams-s3'
      ),
      cacheHit: true
    })
    expect(dependencies.resolveRelease).not.toHaveBeenCalled()
    expect(dependencies.downloadTool).not.toHaveBeenCalled()
  })

  it('rejects an unsupported platform before consulting the cache', async () => {
    const dependencies = createDependencies({
      findTool: jest.fn(() => '/unexpected/cache')
    })

    await expect(
      installCli(
        {
          requestedVersion: '0.1.0',
          platform: 'win32',
          architecture: 'x64'
        },
        dependencies
      )
    ).rejects.toThrow('Unsupported runner platform win32')
    expect(dependencies.findTool).not.toHaveBeenCalled()
  })

  it('downloads, verifies, and caches the latest release', async () => {
    const dependencies = createDependencies()

    const installed = await installCli(
      {
        requestedVersion: 'latest',
        githubToken: 'secret',
        platform: 'linux',
        architecture: 'x64'
      },
      dependencies
    )

    expect(dependencies.resolveRelease).toHaveBeenCalledWith('latest', 'secret')
    expect(dependencies.downloadTool).toHaveBeenCalledTimes(2)
    expect(dependencies.cacheFile).toHaveBeenCalledWith(
      '/tmp/binary',
      'simplestreams-s3',
      'simplestreams-s3',
      '0.1.0',
      'x64'
    )
    expect(dependencies.makeExecutable).toHaveBeenCalledWith(installed.path)
    expect(installed.cacheHit).toBe(false)
  })

  it('fails before caching a checksum mismatch', async () => {
    const dependencies = createDependencies({
      sha256: jest.fn(async () => 'b'.repeat(64))
    })

    await expect(
      installCli(
        {
          requestedVersion: '0.1.0',
          platform: 'linux',
          architecture: 'x64'
        },
        dependencies
      )
    ).rejects.toThrow('Checksum mismatch')
    expect(dependencies.cacheFile).not.toHaveBeenCalled()
  })

  it('rejects a mismatched release tag and missing binary asset', async () => {
    const mismatched = createDependencies({
      resolveRelease: jest.fn(async () => ({ ...release, tag: 'v0.2.0' }))
    })
    await expect(
      installCli(
        {
          requestedVersion: '0.1.0',
          platform: 'linux',
          architecture: 'x64'
        },
        mismatched
      )
    ).rejects.toThrow('does not match requested version')

    const missing = createDependencies({
      resolveRelease: jest.fn(async () => ({ ...release, assets: [] }))
    })
    await expect(
      installCli(
        {
          requestedVersion: 'latest',
          platform: 'linux',
          architecture: 'x64'
        },
        missing
      )
    ).rejects.toThrow('is missing asset simplestreams-s3_0.1.0_linux_amd64')
  })
})
