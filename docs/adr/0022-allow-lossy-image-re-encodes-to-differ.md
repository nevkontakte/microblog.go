# 22. Allow lossy image re-encodes to differ

Date: 2026-11-22

## Status

Accepted

## Context

When the admin uploads an image, the original rebuilds it with Pillow
(`app/uploads.py:49-75`):

1. It applies the EXIF orientation (`ImageOps.exif_transpose`).
2. It copies the pixels into a new image, which drops all metadata, EXIF included. This
   removes data such as GPS positions, so it is a privacy feature.
3. It saves the new image in the upload's format, and a 740-pixel WebP thumbnail.
4. It computes a blurhash from the saved file. The blurhash, the width and the height
   federate in the attachment.

For a lossless format (PNG, GIF is not re-encoded), Go can reproduce the pixels exactly.
For JPEG (Pillow's default quality 75) and WebP, Go's encoders give other bytes than
Pillow's. The stored file, the thumbnail and the blurhash computed from the stored file
then differ from the original's. The gap inventory records this
(`migration/inventory.tsv`, `app/uploads.py`, `save_upload`).

The rulebook allows a tolerated difference only with the owner's explicit approval and
an ADR (`migration/RULEBOOK.md` §0). The owner approved this one in the review of the
inventory ([#40](https://github.com/nevkontakte/microblog.go/pull/40)).

## Decision

- The port keeps steps 1 and 2 exactly: it applies the EXIF orientation and stores no
  metadata. Palette images lose their palette as in the original (a `BUG(port)` in the
  inventory).
- The port re-encodes JPEG and WebP with its own encoders (JPEG at quality 75, as
  Pillow's default), and computes the blurhash from its own re-encoded file.
- The stored bytes and the blurhash of a lossy image need not equal the original's.
  Their pixels must be equal within a noise threshold:
  - Images: decode both and compare per channel. The peak signal-to-noise ratio must be
    at least 35 dB.
  - Blurhash: decode both strings to their 4×3 color components. Each component must be
    within 5% of the full range.

  The judge normalizes attachment bytes and blurhashes of lossy images with these
  checks, and nothing else. Lossless images and every other field stay exact.
- A change of either threshold needs the owner's approval, recorded here.

## Consequences

- The port needs no copy of Pillow's encoders, and stays without cgo
  ([0020](0020-adopt-the-go-dependency-set.md)).
- A receiver shows the same image and a blurhash placeholder with the same colors. Only
  the bytes differ.
- The judge gains an image comparison. It must not hide a real defect, such as a wrong
  orientation or a missing metadata strip, so it compares pixels, not just sizes, and it
  checks for EXIF in the stored file separately.
