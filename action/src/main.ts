import * as core from '@actions/core'
import path from 'node:path'
import { installCli } from './install.js'

/**
 * The main function for the action.
 *
 * @returns Resolves when the action is complete.
 */
export async function run(): Promise<void> {
  try {
    const requestedVersion = core.getInput('version').trim() || 'latest'
    const githubToken = core.getInput('github-token').trim()
    if (githubToken) core.setSecret(githubToken)

    const installed = await installCli({
      requestedVersion,
      githubToken: githubToken || undefined
    })

    core.addPath(path.dirname(installed.path))
    core.info(
      `${installed.cacheHit ? 'Using cached' : 'Installed'} simplestreams-s3 ${installed.version}`
    )
    core.setOutput('cli-version', installed.version)
    core.setOutput('cli-path', installed.path)
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : 'Unexpected error')
  }
}
