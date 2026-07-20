import { jest } from '@jest/globals'
import {
  type ReleaseJsonClient,
  resolvePublishedRelease
} from '../src/github.js'

function clientWith(result: {
  statusCode: number
  result: {
    tag_name: string
    draft: boolean
    prerelease: boolean
    assets: { name: string; browser_download_url: string }[]
  } | null
}): ReleaseJsonClient {
  return { get: jest.fn(async () => result) }
}

describe('resolvePublishedRelease', () => {
  it('resolves the latest public release and maps assets', async () => {
    const client = clientWith({
      statusCode: 200,
      result: {
        tag_name: 'v0.1.0',
        draft: false,
        prerelease: false,
        assets: [
          {
            name: 'checksums.txt',
            browser_download_url: 'https://example.test/checksums.txt'
          }
        ]
      }
    })

    const release = await resolvePublishedRelease('latest', 'secret', client)

    expect(client.get).toHaveBeenCalledWith(
      'https://api.github.com/repos/meigma/simplestreams-s3/releases/latest',
      expect.objectContaining({ Authorization: 'Bearer secret' })
    )
    expect(release).toEqual({
      tag: 'v0.1.0',
      prerelease: false,
      assets: [
        {
          name: 'checksums.txt',
          downloadUrl: 'https://example.test/checksums.txt'
        }
      ]
    })
  })

  it('uses an exact tag lookup without an authorization header', async () => {
    const client = clientWith({
      statusCode: 200,
      result: {
        tag_name: 'v1.2.3-beta.1',
        draft: false,
        prerelease: true,
        assets: []
      }
    })

    await resolvePublishedRelease('1.2.3-beta.1', undefined, client)

    expect(client.get).toHaveBeenCalledWith(
      'https://api.github.com/repos/meigma/simplestreams-s3/releases/tags/v1.2.3-beta.1',
      expect.not.objectContaining({ Authorization: expect.anything() })
    )
  })

  it('rejects draft releases', async () => {
    const client = clientWith({
      statusCode: 200,
      result: {
        tag_name: 'v0.2.0',
        draft: true,
        prerelease: false,
        assets: []
      }
    })

    await expect(
      resolvePublishedRelease('0.2.0', undefined, client)
    ).rejects.toThrow('GitHub release v0.2.0 is still a draft')
  })

  it('reports unsuccessful API responses without response contents', async () => {
    const client = clientWith({ statusCode: 404, result: null })

    await expect(
      resolvePublishedRelease('9.9.9', 'secret', client)
    ).rejects.toThrow('lookup for 9.9.9 failed with status 404')
  })
})
