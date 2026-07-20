import * as core from '@actions/core'
import path from 'node:path'
import { readActionInputs } from './inputs.js'
import { installCli } from './install.js'
import { publishImage } from './publish.js'

/**
 * The main function for the action.
 *
 * @returns Resolves when the action is complete.
 */
export async function run(): Promise<void> {
  try {
    const inputs = readActionInputs()
    if (inputs.githubToken) core.setSecret(inputs.githubToken)

    const installed = await installCli({
      requestedVersion: inputs.requestedVersion,
      githubToken: inputs.githubToken
    })

    core.addPath(path.dirname(installed.path))
    core.info(
      `${installed.cacheHit ? 'Using cached' : 'Installed'} simplestreams-s3 ${installed.version}`
    )
    const published = await publishImage(installed.path, inputs)
    core.setOutput('cli-version', installed.version)
    core.setOutput('cli-path', installed.path)
    core.setOutput('product', published.product)
    core.setOutput('image-version', published.imageVersion)
  } catch (error) {
    core.setFailed(error instanceof Error ? error.message : 'Unexpected error')
  }
}
