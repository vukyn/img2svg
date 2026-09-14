---
name: go-image-decodeconfig-divergence
description: A decoded-pixel guard built on image.DecodeConfig must fail OPEN — x/image/bmp refuses valid RLE/OS-2 BMPs; and VP8X WebP is measurable, so it is the wrong example to cite.
metadata:
  type: reference
---

When bounding *decoded* image size in front of a non-Go tracer/decoder (img2svg's
python+vtracer, any Rust `image`-crate tool), `image.DecodeConfig` is the right
probe — it reads the header only, never allocates the pixel buffer — but the two
decoder stacks do **not** accept the same files, so the unparseable branch must
pass the input through rather than reject it. Rejecting what you merely failed to
MEASURE turns a size guard into a format guard and breaks uploads that work today.

Verified against `golang.org/x/image@v0.46.0`, not assumed:

- **`x/image/bmp` is the real fail-open case.** `reader.go` returns
  `ErrUnsupported` for any `compression != 0` (so every RLE4/RLE8 BMP) and for the
  OS/2 `BITMAPCOREHEADER` (`infoLen` other than 40/108/124). Those are valid BMPs
  that the Rust side reads fine.
- ⚠️ **Extended-container (VP8X) WebP is NOT unmeasurable** — the intuitive example
  is wrong. `x/image/webp` `decode()` has an explicit `case fccVP8X` that reads the
  10-byte chunk, parses the alpha/animation flags and returns the canvas
  dimensions under `configOnly`. It gets measured and capped like anything else. I
  wrote a code comment citing VP8X as the motivating case and had to correct it;
  the justification for a security control has to be a fact, because a comment
  with a wrong reason is what the next reader deletes.

Also: `image.DecodeConfig` needs the decoders registered for side effect at the
call site's package (`_ "image/png"`, `_ "image/jpeg"`, `_ "image/gif"`,
`_ "golang.org/x/image/bmp"`, `_ "golang.org/x/image/webp"`), and a header-only
PNG — 8-byte signature + one IHDR chunk with a CRC32, ~45 bytes total — is enough
for `DecodeConfig` to report any dimensions you like for a truecolor image. That
tiny fixture *is* the attack shape (encoded size says nothing about decoded size),
so build test inputs that way instead of encoding real megapixel images.

Related: [[feedback-prove-regression-tests]] — the guard's over/under/unmeasurable
branches each need their own mutation, especially the per-side dimension check,
which a pure area test never exercises.
