# App icon

`cliproxyapi.svg` is original geometric artwork for this wrapper repository,
created as part of this implementation and distributed under the repository's
MIT license. It is not an OpenAI, Unraid, or upstream CLIProxyAPI logo.
`cliproxyapi.png` is the 256 x 256 PNG export used by the Unraid template.

Rebuild the PNG with librsvg:

```sh
rsvg-convert assets/cliproxyapi.svg -o assets/cliproxyapi.png
```

After publishing the repository, verify this raw URL returns the PNG without
authentication:

https://raw.githubusercontent.com/OzoneH3/CLIProxyAPI_unraid/main/assets/cliproxyapi.png
