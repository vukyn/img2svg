"""decheck — strip a baked-in transparency chequerboard before tracing.

An image saved as JPEG cannot carry transparency, so a transparent PNG saved that
way keeps the chequerboard the *viewer* was painting behind it. Nothing about the
file says so: the chequer is ordinary pixels by then. Trace it and the vector
comes out with a grey-and-white background wrapped around the subject, which is
the one thing a cut-out asset must not have.

# Why it is not a colour to erase

The obvious version — make every near-white pixel transparent — punches holes
through the drawing. An eye highlight, a white fur collar and a metal headband
are all pale and unsaturated, and all three came out of the first attempt at this
with pieces missing. The second version flood-filled from the border instead,
which spares them, because a drawing's own whites are enclosed by its outlines
and the border cannot reach them.

That misses the other half: a chequer patch enclosed between an arm and a coat is
background the border cannot reach either.

# What tells the two apart

The grid. A chequer alternates on a fixed pitch, and a drawing's shading does not,
however many tones it carries. The pitch is fitted from the flood — where the
background is certainly chequer — and an enclosed patch is cut only when nine
tenths of it agrees with that pitch.

When no pitch fits, there is no chequer to be confident about and only the flood
runs. That is the safe way round: a picture keeps a background it did not need to
lose, rather than losing a collar it did.
"""
from collections import Counter, deque

# A chequer square is pale and unsaturated. The bounds are generous because JPEG
# moves both: the light square lands near 255 and the darker one anywhere from
# about 200 to 240, depending on how hard the file was compressed.
MAX_CHROMA = 26
MIN_VALUE = 190
WHITE = 245

# How much of an enclosed patch must agree with the fitted grid before it is
# called chequer. High, because the whole point is to spare a drawing's greys.
AGREEMENT = 0.9
# And how much of the flood must agree before there is a grid at all.
PRESENT = 0.85


def _pale(pixel):
    r, g, b = pixel
    return max(r, g, b) - min(r, g, b) < MAX_CHROMA and min(r, g, b) > MIN_VALUE


def _tone(pixel):
    return 1 if min(pixel) >= WHITE else 0


def _flood_from_border(px, w, h):
    """Every pale pixel the border can reach. This half is certainly background."""
    found = bytearray(w * h)
    queue = deque()
    edge = [(x, y) for x in range(w) for y in (0, h - 1)]
    edge += [(x, y) for y in range(h) for x in (0, w - 1)]
    for x, y in edge:
        if _pale(px[x, y]) and not found[y * w + x]:
            found[y * w + x] = 1
            queue.append((x, y))
    while queue:
        x, y = queue.popleft()
        for nx, ny in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
            if 0 <= nx < w and 0 <= ny < h and not found[ny * w + nx] and _pale(px[nx, ny]):
                found[ny * w + nx] = 1
                queue.append((nx, ny))
    return found


def _fit_grid(px, w, h, found):
    """The chequer's square size and offset, or None when there is no chequer.

    The size comes from run lengths along the flood's own rows: an alternation on
    a fixed pitch makes that pitch the commonest run. Everything after that is a
    search over one number's worth of offsets.
    """
    runs = Counter()
    for y in range(0, h, 3):
        run, last = 0, None
        for x in range(w):
            if not found[y * w + x]:
                run, last = 0, None
                continue
            here = _tone(px[x, y])
            if here == last:
                run += 1
                continue
            if 2 < run < 64:
                runs[run] += 1
            run, last = 1, here
    if not runs:
        return None
    size = runs.most_common(1)[0][0]

    sample = [(x, y) for y in range(0, h, 2) for x in range(0, w, 2) if found[y * w + x]]
    if len(sample) < 64:
        return None
    best = None
    for ox in range(size):
        for oy in range(size):
            agree = sum(1 for x, y in sample
                        if _tone(px[x, y]) == (((x + ox) // size + (y + oy) // size) % 2))
            # Either phase of the two tones is a chequer; which one is which is
            # the viewer's choice and nothing here should care.
            for score, flipped in ((agree, False), (len(sample) - agree, True)):
                if best is None or score > best[0]:
                    best = (score, size, ox, oy, flipped)
    if best[0] / len(sample) < PRESENT:
        return None
    return best[1], best[2], best[3], best[4]


def _on_grid(px, grid, x, y):
    size, ox, oy, flipped = grid
    want = ((x + ox) // size + (y + oy) // size) % 2
    return _tone(px[x, y]) == (1 - want if flipped else want)


def _cut_enclosed(px, w, h, cut, grid):
    """Cut the pale patches the border could not reach but the grid explains."""
    holes = 0
    for start_y in range(h):
        for start_x in range(w):
            if cut[start_y * w + start_x] or not _pale(px[start_x, start_y]):
                continue
            queue, region = deque([(start_x, start_y)]), []
            cut[start_y * w + start_x] = 1
            while queue:
                x, y = queue.popleft()
                region.append((x, y))
                for nx, ny in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
                    if 0 <= nx < w and 0 <= ny < h and not cut[ny * w + nx] and _pale(px[nx, ny]):
                        cut[ny * w + nx] = 1
                        queue.append((nx, ny))
            # Four squares' worth at least, or a blob sitting inside a single
            # cell would "agree" with the grid by having nowhere to disagree.
            enough = len(region) >= 4 * grid[0] * grid[0]
            agree = sum(1 for x, y in region if _on_grid(px, grid, x, y))
            if not enough or agree / len(region) < AGREEMENT:
                for x, y in region:
                    cut[y * w + x] = 0
                continue
            holes += len(region)
    return holes


def _feather(px, w, h, cut):
    """One step into the boundary, which JPEG left as a blend of both sides.

    Traced, that blend is a halo following the whole silhouette. It goes by colour
    rather than by the grid, because it is neither tone any more.
    """
    edge = []
    for y in range(h):
        for x in range(w):
            if cut[y * w + x]:
                continue
            r, g, b = px[x, y]
            if max(r, g, b) - min(r, g, b) < 40 and min(r, g, b) > 170:
                for nx, ny in ((x + 1, y), (x - 1, y), (x, y + 1), (x, y - 1)):
                    if 0 <= nx < w and 0 <= ny < h and cut[ny * w + nx]:
                        edge.append(y * w + x)
                        break
    for index in edge:
        cut[index] = 1
    return len(edge)


def decheck(image):
    """Return (RGBA image with the chequer made transparent, report dict)."""
    from PIL import Image

    rgb = image.convert("RGB")
    w, h = rgb.size
    px = rgb.load()

    cut = _flood_from_border(px, w, h)
    grid = _fit_grid(px, w, h, cut)
    holes = _cut_enclosed(px, w, h, cut, grid) if grid else 0
    feathered = _feather(px, w, h, cut)

    out = Image.new("RGBA", (w, h))
    op = out.load()
    gone = 0
    for y in range(h):
        for x in range(w):
            if cut[y * w + x]:
                op[x, y] = (0, 0, 0, 0)
                gone += 1
            else:
                op[x, y] = px[x, y] + (255,)
    return out, {"cut": gone, "pixels": w * h, "grid": grid,
                 "enclosed": holes, "feathered": feathered}
