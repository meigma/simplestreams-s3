import { jest } from '@jest/globals'
import { type ActionInputs } from '../src/inputs.js'
import {
  buildPublishArguments,
  parsePublishResult,
  publishImage,
  type PublishExecutor
} from '../src/publish.js'

const minimalInputs: ActionInputs = {
  requestedVersion: '0.1.0',
  metadataPath: '/images/incus.tar.xz',
  diskPath: '/images/disk.qcow2',
  s3Bucket: 'private-images',
  aliases: []
}

describe('publish arguments', () => {
  it('builds the minimal fixed command without CLI-owned defaults', () => {
    expect(buildPublishArguments(minimalInputs)).toEqual([
      'publish',
      '--s3-bucket',
      'private-images',
      '/images/incus.tar.xz',
      '/images/disk.qcow2'
    ])
  })

  it('maps every optional input and emits one flag per alias', () => {
    expect(
      buildPublishArguments({
        ...minimalInputs,
        configFile: '/config/publish.yml',
        s3Prefix: 'mirrors/incus',
        s3Region: 'us-west-2',
        s3ExpectedBucketOwner: '123456789012',
        evidenceManifestPath: '/evidence/evidence-manifest.json',
        aliases: ['alpine/stable', 'alpine/latest'],
        releaseTitle: 'Alpine 3.22',
        publishTimeout: '1h',
        catalogTimeout: '45s',
        catalogAttempts: '6',
        s3MaxAttempts: '5',
        s3MaxBackoff: '2s',
        s3DialTimeout: '4s',
        s3TlsHandshakeTimeout: '6s',
        s3ResponseHeaderTimeout: '7s'
      })
    ).toEqual([
      'publish',
      '--s3-bucket',
      'private-images',
      '--config',
      '/config/publish.yml',
      '--s3-prefix',
      'mirrors/incus',
      '--s3-region',
      'us-west-2',
      '--s3-expected-bucket-owner',
      '123456789012',
      '--evidence-manifest',
      '/evidence/evidence-manifest.json',
      '--release-title',
      'Alpine 3.22',
      '--publish-timeout',
      '1h',
      '--catalog-timeout',
      '45s',
      '--catalog-attempts',
      '6',
      '--s3-max-attempts',
      '5',
      '--s3-max-backoff',
      '2s',
      '--s3-dial-timeout',
      '4s',
      '--s3-tls-handshake-timeout',
      '6s',
      '--s3-response-header-timeout',
      '7s',
      '--alias',
      'alpine/stable',
      '--alias',
      'alpine/latest',
      '/images/incus.tar.xz',
      '/images/disk.qcow2'
    ])
  })
})

describe('publish execution', () => {
  it('executes the binary directly and parses the exact success line', async () => {
    const execute = jest.fn<PublishExecutor>().mockResolvedValue({
      exitCode: 0,
      stdout: 'published alpinelinux:3.22:cloud:arm64 version 202607181302\n',
      stderr: ''
    })

    await expect(
      publishImage('/tool-cache/simplestreams-s3', minimalInputs, execute)
    ).resolves.toEqual({
      product: 'alpinelinux:3.22:cloud:arm64',
      imageVersion: '202607181302'
    })
    expect(execute).toHaveBeenCalledWith(
      '/tool-cache/simplestreams-s3',
      buildPublishArguments(minimalInputs),
      { ignoreReturnCode: true }
    )
  })

  it('propagates a non-zero CLI exit without parsing output', async () => {
    const execute = jest.fn<PublishExecutor>().mockResolvedValue({
      exitCode: 7,
      stdout: '',
      stderr: 'access denied'
    })

    await expect(
      publishImage('/tool-cache/simplestreams-s3', minimalInputs, execute)
    ).rejects.toThrow('simplestreams-s3 publish exited with code 7')
  })
})

describe('publish result parsing', () => {
  it('accepts one exact result among other lines', () => {
    expect(
      parsePublishResult(
        'preparing\r\npublished ubuntu:24.04:cloud:amd64 version 202607201530\r\n'
      )
    ).toEqual({
      product: 'ubuntu:24.04:cloud:amd64',
      imageVersion: '202607201530'
    })
  })

  it.each([
    '',
    'published product version dev',
    ' published product version 202607201530',
    'published product version 202607201530 ',
    'published product version 202607201530 extra',
    'published a version 202607201530\npublished b version 202607201531\n'
  ])('rejects ambiguous or malformed output: %j', (stdout) => {
    expect(() => parsePublishResult(stdout)).toThrow(
      'did not emit exactly one valid result line'
    )
  })
})
