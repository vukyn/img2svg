#!/usr/bin/env python3
"""img2svg — trace a raster image (Pokemon art, icons, logos) into vector SVG.

Wraps vtracer color tracing. Vector output = scale infinite, no blur.

Modes:
    file  : img2svg.py <image> [-o out.svg]      # out defaults to <image>.svg
    stdout: img2svg.py <image> -o -              # SVG to stdout
    pipe  : img2svg.py - [-q ...]                # read image bytes from stdin, SVG to stdout

The pipe mode is what the Go service uses (os/exec): image bytes in via stdin,
SVG text out via stdout. No temp files.

    python3 img2svg.py bulbasaur.png             # -> bulbasaur.svg (faithful)
    python3 img2svg.py art.webp -o icon.svg -q small
    cat art.png | python3 img2svg.py - -q balanced > art.svg
    python3 img2svg.py naruto.jpeg --decheck     # strip a baked-in chequerboard first
"""
import argparse
import io
import os
import sys

# quality presets: fewer layers + bigger speckle filter = smaller file, less detail
PRESETS = {
    "faithful": dict(filter_speckle=4, color_precision=7, layer_difference=14, path_precision=6),
    "balanced": dict(filter_speckle=8, color_precision=6, layer_difference=22, path_precision=5),
    "small":    dict(filter_speckle=16, color_precision=5, layer_difference=32, path_precision=4),
}

# magic-byte signatures for stdin format detection
_MAGIC = [
    (b"\x89PNG\r\n\x1a\n", "png"),
    (b"\xff\xd8\xff", "jpg"),
    (b"GIF87a", "gif"),
    (b"GIF89a", "gif"),
    (b"BM", "bmp"),
]


def detect_format(data):
    for sig, fmt in _MAGIC:
        if data.startswith(sig):
            return fmt
    if data[:4] == b"RIFF" and data[8:12] == b"WEBP":
        return "webp"
    return None


def _vtracer():
    try:
        import vtracer
        return vtracer
    except ImportError:
        sys.exit("vtracer not installed. Run: python3 -m pip install -r cli/requirements.txt")


def dechecked(data):
    """Return PNG bytes with a baked-in transparency chequerboard removed.

    Wanted for one thing and one thing only: a transparent PNG that somebody
    saved as JPEG. JPEG cannot carry transparency, so what it keeps is the
    chequerboard the *viewer* was painting behind the image -- and by then that
    is ordinary pixels with nothing to say it was ever a background. Traced, it
    comes out as a grey-and-white pattern wrapped around the subject.

    Off by default. It is a repair, and an image that never had the problem
    should not be put through a filter that could take a white collar off it.
    """
    try:
        from PIL import Image
    except ImportError:
        sys.exit("--decheck needs Pillow. Run: python3 -m pip install -r cli/requirements.txt")
    from decheck import decheck

    out, report = decheck(Image.open(io.BytesIO(data)))
    if report["grid"] is None:
        print("decheck: no chequer grid fitted; only the border flood ran",
              file=sys.stderr)
    else:
        size, _, _, _ = report["grid"]
        print("decheck: %d px squares, cut %.1f%% (%d enclosed, %d feathered)" % (
            size, report["cut"] * 100.0 / report["pixels"],
            report["enclosed"], report["feathered"]), file=sys.stderr)
    buffer = io.BytesIO()
    out.save(buffer, format="PNG")
    return buffer.getvalue()


def trace_bytes(data, preset):
    """Trace raw image bytes -> SVG string (used by pipe mode)."""
    fmt = detect_format(data)
    if fmt is None:
        sys.exit("unrecognized image format (want png/jpg/gif/bmp/webp)")
    return _vtracer().convert_raw_image_to_svg(
        data, img_format=fmt,
        colormode="color", hierarchical="stacked", mode="spline",
        corner_threshold=60, length_threshold=4.0, splice_threshold=45,
        **PRESETS[preset],
    )


def trace_file(image_path, out_path, preset):
    """Trace an image file -> out_path file (used by file mode)."""
    _vtracer().convert_image_to_svg_py(
        image_path, out_path,
        colormode="color", hierarchical="stacked", mode="spline",
        corner_threshold=60, length_threshold=4.0, splice_threshold=45,
        **PRESETS[preset],
    )


def main():
    p = argparse.ArgumentParser(description="Trace a raster image into an SVG.")
    p.add_argument("image", help="input image path, or '-' to read bytes from stdin")
    p.add_argument("-o", "--out", help="output .svg path, or '-' for stdout (default: <image>.svg)")
    p.add_argument("-q", "--quality", choices=list(PRESETS), default="faithful",
                   help="faithful (exact, big) | balanced | small (default: faithful)")
    p.add_argument("--decheck", action="store_true",
                   help="strip a baked-in transparency chequerboard before tracing "
                        "(for a transparent PNG that was saved as JPEG)")
    args = p.parse_args()

    # pipe mode: stdin bytes -> stdout SVG
    if args.image == "-":
        data = sys.stdin.buffer.read()
        if not data:
            sys.exit("no bytes on stdin")
        if args.decheck:
            data = dechecked(data)
        sys.stdout.write(trace_bytes(data, args.quality))
        return

    if not os.path.isfile(args.image):
        sys.exit(f"no such file: {args.image}")

    # stdout mode: file in, SVG to stdout
    if args.out == "-":
        with open(args.image, "rb") as f:
            data = f.read()
        if args.decheck:
            data = dechecked(data)
        sys.stdout.write(trace_bytes(data, args.quality))
        return

    # file mode
    out = args.out or os.path.splitext(args.image)[0] + ".svg"
    if args.decheck:
        # Through the byte path rather than the file one, so the repair happens
        # in memory and no half-fixed PNG is left beside the source.
        with open(args.image, "rb") as f:
            svg = trace_bytes(dechecked(f.read()), args.quality)
        with open(out, "w") as f:
            f.write(svg)
        print(f"{out}  ({os.path.getsize(out) / 1024:.0f} KB, {args.quality}, dechecked)",
              file=sys.stderr)
        return
    trace_file(args.image, out, args.quality)
    print(f"{out}  ({os.path.getsize(out) / 1024:.0f} KB, {args.quality})", file=sys.stderr)


if __name__ == "__main__":
    main()
