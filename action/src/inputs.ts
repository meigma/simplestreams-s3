import * as core from '@actions/core'

export interface ActionInputs {
  requestedVersion: string
  githubToken?: string
  metadataPath: string
  diskPath: string
  s3Bucket: string
  configFile?: string
  s3Prefix?: string
  s3Region?: string
  s3ExpectedBucketOwner?: string
  aliases: string[]
  releaseTitle?: string
  publishTimeout?: string
  catalogTimeout?: string
  catalogAttempts?: string
  s3MaxAttempts?: string
  s3MaxBackoff?: string
  s3DialTimeout?: string
  s3TlsHandshakeTimeout?: string
  s3ResponseHeaderTimeout?: string
}

/** Read the fixed action interface without inventing defaults owned by the CLI. */
export function readActionInputs(): ActionInputs {
  return {
    requestedVersion: optionalInput('version') || 'latest',
    githubToken: optionalInput('github-token'),
    metadataPath: requiredInput('metadata-path'),
    diskPath: requiredInput('disk-path'),
    s3Bucket: requiredInput('s3-bucket'),
    configFile: optionalInput('config-file'),
    s3Prefix: optionalInput('s3-prefix'),
    s3Region: optionalInput('s3-region'),
    s3ExpectedBucketOwner: optionalInput('s3-expected-bucket-owner'),
    aliases: core
      .getMultilineInput('aliases', { trimWhitespace: true })
      .filter(Boolean),
    releaseTitle: optionalInput('release-title'),
    publishTimeout: optionalInput('publish-timeout'),
    catalogTimeout: optionalInput('catalog-timeout'),
    catalogAttempts: optionalInput('catalog-attempts'),
    s3MaxAttempts: optionalInput('s3-max-attempts'),
    s3MaxBackoff: optionalInput('s3-max-backoff'),
    s3DialTimeout: optionalInput('s3-dial-timeout'),
    s3TlsHandshakeTimeout: optionalInput('s3-tls-handshake-timeout'),
    s3ResponseHeaderTimeout: optionalInput('s3-response-header-timeout')
  }
}

function requiredInput(name: string): string {
  return core.getInput(name, { required: true, trimWhitespace: true })
}

function optionalInput(name: string): string | undefined {
  return (
    core.getInput(name, { required: false, trimWhitespace: true }) || undefined
  )
}
