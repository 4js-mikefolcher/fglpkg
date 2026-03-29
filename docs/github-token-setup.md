# Creating a GitHub Fine-Grained Personal Access Token for fglpkg

Package zips are stored as GitHub Release assets on a private repository. Both publishers and consumers need a GitHub token to access them.

## Steps

1. Go to [github.com](https://github.com) and click your profile picture, then **Settings**
2. Scroll down the left sidebar and click **Developer settings**
3. Click **Personal access tokens**, then **Fine-grained tokens**
4. Click **Generate new token**
5. Fill in the token details:
   - **Token name**: a descriptive name (e.g., `fglpkg-publish` or `fglpkg-read`)
   - **Expiration**: choose a duration (e.g., 90 days, 1 year)
   - **Repository access**: select **Only select repositories**, then choose your packages repository (e.g., `fglpkg-packages`)
   - **Permissions**: expand **Repository permissions**, find **Contents**, and set the access level:
     - **Read and write** for publishers (needed to create releases and upload assets)
     - **Read-only** for consumers who only need to install packages
6. Click **Generate token**
7. Copy the token immediately (it starts with `github_pat_...`) -- it will not be shown again

## Using the Token

### Interactive (stored in credentials file)

```bash
fglpkg login
# When prompted for "GitHub token", paste the token
```

### Environment variable (CI/CD)

```bash
export FGLPKG_GITHUB_TOKEN=github_pat_xxxxxxxxxxxx
```

## Token Permissions Summary

| Role | Contents Permission | Can Publish | Can Install |
|---|---|---|---|
| Publisher | Read and write | Yes | Yes |
| Consumer | Read-only | No | Yes |
