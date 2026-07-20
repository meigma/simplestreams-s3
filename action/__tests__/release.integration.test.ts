import { execFile } from 'node:child_process'
import { mkdtemp, mkdir } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { promisify } from 'node:util'
import { installCli } from '../src/install.js'

const execFileAsync = promisify(execFile)
const integrationTest =
  process.env.SIMPLESTREAMS_S3_ACTION_RELEASE_TEST === '1'
    ? describe
    : describe.skip

integrationTest('public release installation', () => {
  it('resolves latest, executes v0.1.0, and reuses it on Linux', async () => {
    expect(process.platform).toBe('linux')
    const root = await mkdtemp(
      path.join(os.tmpdir(), 'simplestreams-s3-action-')
    )
    const toolCache = path.join(root, 'tool-cache')
    const runnerTemp = path.join(root, 'runner-temp')
    await Promise.all([mkdir(toolCache), mkdir(runnerTemp)])
    process.env.RUNNER_TOOL_CACHE = toolCache
    process.env.RUNNER_TEMP = runnerTemp

    const installed = await installCli({ requestedVersion: 'latest' })
    expect(installed).toMatchObject({ version: '0.1.0', cacheHit: false })

    const executed = await execFileAsync(installed.path, ['--version'])
    expect(executed.stdout).toMatch(
      /^simplestreams-s3 0\.1\.0 \([0-9a-f]+\) built /u
    )

    const repeated = await installCli({ requestedVersion: 'v0.1.0' })
    expect(repeated).toEqual({ ...installed, cacheHit: true })
  }, 120_000)
})
