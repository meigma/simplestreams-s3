import { jest } from '@jest/globals'
import * as core from '../__fixtures__/core.js'

const installCli = jest.fn<
  () => Promise<{
    version: string
    path: string
    cacheHit: boolean
  }>
>()

jest.unstable_mockModule('@actions/core', () => core)
jest.unstable_mockModule('../src/install.js', () => ({ installCli }))

const { run } = await import('../src/main.js')

describe('run', () => {
  beforeEach(() => {
    core.getInput.mockImplementation((name) => {
      if (name === 'version') return 'v0.1.0'
      if (name === 'github-token') return 'secret'
      return ''
    })
    installCli.mockResolvedValue({
      version: '0.1.0',
      path: '/toolcache/0.1.0/x64/simplestreams-s3',
      cacheHit: false
    })
  })

  afterEach(() => {
    jest.resetAllMocks()
  })

  it('masks the token, adds the CLI to PATH, and sets outputs', async () => {
    await run()

    expect(core.setSecret).toHaveBeenCalledWith('secret')
    expect(installCli).toHaveBeenCalledWith({
      requestedVersion: 'v0.1.0',
      githubToken: 'secret'
    })
    expect(core.addPath).toHaveBeenCalledWith('/toolcache/0.1.0/x64')
    expect(core.setOutput).toHaveBeenCalledWith('cli-version', '0.1.0')
    expect(core.setOutput).toHaveBeenCalledWith(
      'cli-path',
      '/toolcache/0.1.0/x64/simplestreams-s3'
    )
    expect(core.setFailed).not.toHaveBeenCalled()
  })

  it('uses latest without a token and reports installation errors', async () => {
    core.getInput.mockImplementation(() => '')
    installCli.mockRejectedValue(new Error('download failed'))

    await run()

    expect(core.setSecret).not.toHaveBeenCalled()
    expect(installCli).toHaveBeenCalledWith({
      requestedVersion: 'latest',
      githubToken: undefined
    })
    expect(core.setFailed).toHaveBeenCalledWith('download failed')
  })
})
