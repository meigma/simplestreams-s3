import { jest } from '@jest/globals'
import * as core from '../__fixtures__/core.js'

jest.unstable_mockModule('@actions/core', () => core)

const { readActionInputs } = await import('../src/inputs.js')

describe('readActionInputs', () => {
  afterEach(() => {
    jest.resetAllMocks()
  })

  it('reads required inputs, normalizes aliases, and leaves CLI defaults empty', () => {
    core.getInput.mockImplementation((name) => {
      const values: Record<string, string> = {
        'metadata-path': '/images/incus.tar.xz',
        'disk-path': '/images/disk.qcow2',
        's3-bucket': 'private-images',
        's3-region': 'us-west-2'
      }
      return values[name] ?? ''
    })
    core.getMultilineInput.mockReturnValue([
      'alpinelinux/stable',
      '',
      'alpinelinux/latest'
    ])

    expect(readActionInputs()).toEqual({
      requestedVersion: 'latest',
      githubToken: undefined,
      metadataPath: '/images/incus.tar.xz',
      diskPath: '/images/disk.qcow2',
      evidenceManifestPath: undefined,
      s3Bucket: 'private-images',
      configFile: undefined,
      s3Prefix: undefined,
      s3Region: 'us-west-2',
      s3ExpectedBucketOwner: undefined,
      aliases: ['alpinelinux/stable', 'alpinelinux/latest'],
      releaseTitle: undefined,
      publishTimeout: undefined,
      catalogTimeout: undefined,
      catalogAttempts: undefined,
      s3MaxAttempts: undefined,
      s3MaxBackoff: undefined,
      s3DialTimeout: undefined,
      s3TlsHandshakeTimeout: undefined,
      s3ResponseHeaderTimeout: undefined
    })
    expect(core.getInput).toHaveBeenCalledWith('metadata-path', {
      required: true,
      trimWhitespace: true
    })
    expect(core.getMultilineInput).toHaveBeenCalledWith('aliases', {
      trimWhitespace: true
    })
  })

  it('passes every optional value through unchanged', () => {
    core.getInput.mockImplementation((name) => name)
    core.getMultilineInput.mockReturnValue([])

    expect(readActionInputs()).toMatchObject({
      requestedVersion: 'version',
      githubToken: 'github-token',
      configFile: 'config-file',
      evidenceManifestPath: 'evidence-manifest-path',
      s3Prefix: 's3-prefix',
      releaseTitle: 'release-title',
      s3ResponseHeaderTimeout: 's3-response-header-timeout'
    })
  })
})
