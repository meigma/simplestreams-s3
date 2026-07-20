import * as actionsExec from '@actions/exec'
import { type ActionInputs } from './inputs.js'

export interface PublishResult {
  product: string
  imageVersion: string
}

export type PublishExecutor = typeof actionsExec.getExecOutput

const optionalFlags: ReadonlyArray<
  readonly [keyof ActionInputs, `--${string}`]
> = [
  ['configFile', '--config'],
  ['s3Prefix', '--s3-prefix'],
  ['s3Region', '--s3-region'],
  ['s3ExpectedBucketOwner', '--s3-expected-bucket-owner'],
  ['releaseTitle', '--release-title'],
  ['publishTimeout', '--publish-timeout'],
  ['catalogTimeout', '--catalog-timeout'],
  ['catalogAttempts', '--catalog-attempts'],
  ['s3MaxAttempts', '--s3-max-attempts'],
  ['s3MaxBackoff', '--s3-max-backoff'],
  ['s3DialTimeout', '--s3-dial-timeout'],
  ['s3TlsHandshakeTimeout', '--s3-tls-handshake-timeout'],
  ['s3ResponseHeaderTimeout', '--s3-response-header-timeout']
]

/** Build the only CLI invocation supported by this action. */
export function buildPublishArguments(inputs: ActionInputs): string[] {
  const arguments_: string[] = ['publish', '--s3-bucket', inputs.s3Bucket]

  for (const [key, flag] of optionalFlags) {
    const value = inputs[key]
    if (typeof value === 'string' && value) arguments_.push(flag, value)
  }
  for (const alias of inputs.aliases) arguments_.push('--alias', alias)

  arguments_.push(inputs.metadataPath, inputs.diskPath)
  return arguments_
}

/** Execute publish directly, forwarding output while capturing its stable result line. */
export async function publishImage(
  cliPath: string,
  inputs: ActionInputs,
  execute: PublishExecutor = actionsExec.getExecOutput
): Promise<PublishResult> {
  const result = await execute(cliPath, buildPublishArguments(inputs), {
    ignoreReturnCode: true
  })
  if (result.exitCode !== 0) {
    throw new Error(
      `simplestreams-s3 publish exited with code ${result.exitCode}`
    )
  }
  return parsePublishResult(result.stdout)
}

/** Parse exactly one successful CLI result without treating other output as data. */
export function parsePublishResult(stdout: string): PublishResult {
  const matches = stdout
    .split(/\r?\n/u)
    .map((line) => /^published (\S+) version (\d{12})$/u.exec(line))
    .filter((match): match is RegExpExecArray => match !== null)

  if (matches.length !== 1) {
    throw new Error(
      'simplestreams-s3 publish did not emit exactly one valid result line'
    )
  }
  return { product: matches[0][1], imageVersion: matches[0][2] }
}
