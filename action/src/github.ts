import { HttpClient } from '@actions/http-client'

const releasesBaseUrl =
  'https://api.github.com/repos/meigma/simplestreams-s3/releases'

interface GitHubAsset {
  name: string
  browser_download_url: string
}

interface GitHubRelease {
  tag_name: string
  draft: boolean
  prerelease: boolean
  assets: GitHubAsset[]
}

export interface ReleaseAsset {
  name: string
  downloadUrl: string
}

export interface PublishedRelease {
  tag: string
  prerelease: boolean
  assets: ReleaseAsset[]
}

interface JsonResponse<T> {
  statusCode: number
  result: T | null
}

export interface ReleaseJsonClient {
  get(
    url: string,
    headers: Record<string, string>
  ): Promise<JsonResponse<GitHubRelease>>
}

class GitHubJsonClient implements ReleaseJsonClient {
  private readonly client = new HttpClient('simplestreams-s3-action')

  async get(
    url: string,
    headers: Record<string, string>
  ): Promise<JsonResponse<GitHubRelease>> {
    return await this.client.getJson<GitHubRelease>(url, headers)
  }
}

/** Resolve one public release and its downloadable assets. */
export async function resolvePublishedRelease(
  requestedVersion: string,
  githubToken?: string,
  client: ReleaseJsonClient = new GitHubJsonClient()
): Promise<PublishedRelease> {
  const selector =
    requestedVersion === 'latest'
      ? 'latest'
      : `tags/v${encodeURIComponent(requestedVersion)}`
  const headers: Record<string, string> = {
    Accept: 'application/vnd.github+json',
    'X-GitHub-Api-Version': '2022-11-28'
  }
  if (githubToken) headers.Authorization = `Bearer ${githubToken}`

  const response = await client.get(`${releasesBaseUrl}/${selector}`, headers)
  if (response.statusCode !== 200 || response.result === null) {
    throw new Error(
      `GitHub release lookup for ${requestedVersion} failed with status ${response.statusCode}`
    )
  }
  if (response.result.draft) {
    throw new Error(
      `GitHub release ${response.result.tag_name} is still a draft`
    )
  }

  return {
    tag: response.result.tag_name,
    prerelease: response.result.prerelease,
    assets: response.result.assets.map((asset) => ({
      name: asset.name,
      downloadUrl: asset.browser_download_url
    }))
  }
}
