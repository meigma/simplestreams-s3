import { jest } from '@jest/globals'
import * as core from '../__fixtures__/core.js'
import { type ActionInputs } from '../src/inputs.js'

const inputs: ActionInputs = {
  requestedVersion: '0.1.0',
  githubToken: 'secret',
  metadataPath: '/images/incus.tar.xz',
  diskPath: '/images/disk.qcow2',
  s3Bucket: 'private-images',
  aliases: []
}
const readActionInputs = jest.fn(() => inputs)
const installCli = jest.fn<
  () => Promise<{
    version: string
    path: string
    cacheHit: boolean
  }>
>()
const publishImage =
  jest.fn<() => Promise<{ product: string; imageVersion: string }>>()

jest.unstable_mockModule('@actions/core', () => core)
jest.unstable_mockModule('../src/inputs.js', () => ({ readActionInputs }))
jest.unstable_mockModule('../src/install.js', () => ({ installCli }))
jest.unstable_mockModule('../src/publish.js', () => ({ publishImage }))

const { run } = await import('../src/main.js')

describe('run', () => {
  beforeEach(() => {
    readActionInputs.mockReturnValue(inputs)
    installCli.mockResolvedValue({
      version: '0.1.0',
      path: '/toolcache/0.1.0/x64/simplestreams-s3',
      cacheHit: false
    })
    publishImage.mockResolvedValue({
      product: 'alpinelinux:3.22:cloud:arm64',
      imageVersion: '202607181302'
    })
  })

  afterEach(() => {
    jest.resetAllMocks()
  })

  it('installs, publishes, and exposes the complete successful result', async () => {
    await run()

    expect(core.setSecret).toHaveBeenCalledWith('secret')
    expect(installCli).toHaveBeenCalledWith({
      requestedVersion: '0.1.0',
      githubToken: 'secret'
    })
    expect(core.addPath).toHaveBeenCalledWith('/toolcache/0.1.0/x64')
    expect(publishImage).toHaveBeenCalledWith(
      '/toolcache/0.1.0/x64/simplestreams-s3',
      inputs
    )
    expect(core.setOutput).toHaveBeenCalledWith('cli-version', '0.1.0')
    expect(core.setOutput).toHaveBeenCalledWith(
      'cli-path',
      '/toolcache/0.1.0/x64/simplestreams-s3'
    )
    expect(core.setOutput).toHaveBeenCalledWith(
      'product',
      'alpinelinux:3.22:cloud:arm64'
    )
    expect(core.setOutput).toHaveBeenCalledWith('image-version', '202607181302')
    expect(core.setFailed).not.toHaveBeenCalled()
  })

  it('does not mask an absent token and reports publish failures', async () => {
    const tokenless = { ...inputs, githubToken: undefined }
    readActionInputs.mockReturnValue(tokenless)
    publishImage.mockRejectedValue(new Error('publication failed'))

    await run()

    expect(core.setSecret).not.toHaveBeenCalled()
    expect(core.setFailed).toHaveBeenCalledWith('publication failed')
    expect(core.setOutput).not.toHaveBeenCalled()
  })
})
