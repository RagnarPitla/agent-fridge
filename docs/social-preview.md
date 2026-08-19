# GitHub social preview

Agent Fridge includes an upload-ready repository social preview and its editable
source:

- Upload image: [`docs/assets/social-preview.png`](assets/social-preview.png)
- Editable source: [`docs/assets/social-preview.svg`](assets/social-preview.svg)

The PNG is 1280x640 pixels, uses the sRGB color profile, and stays below
GitHub's 1 MB upload limit.

## Upload through GitHub

Repository settings are not changed by this branch. After the pull request is
reviewed and merged, a repository administrator can upload the image:

1. Open the repository on GitHub.
2. Go to **Settings -> General**.
3. Find **Social preview**.
4. Select **Edit -> Upload image**.
5. Choose `docs/assets/social-preview.png`.
6. Confirm the preview and save if GitHub asks.

GitHub accepts PNG, JPG, or GIF images below 1 MB and recommends 1280x640 for
best display. See
[GitHub's social preview documentation](https://docs.github.com/en/repositories/managing-your-repositorys-settings-and-features/customizing-your-repository/customizing-your-repositorys-social-media-preview).

Do not upload the SVG. Keep it in the repository as the editable source for
future brand updates.
