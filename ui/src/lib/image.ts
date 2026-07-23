import type { FileMeta, ResizeMode } from "../types";

// The result of preparing an image for tracing: the bytes to upload plus the
// final dimensions and which transforms were applied. `null` from prepareInput
// means "no canvas step needed — upload the original file as-is".
export interface PreparedInput {
	blob: Blob;
	w: number;
	h: number;
	resized: boolean;
	transparent: boolean;
}

// Target size for the current resize mode, or null = keep original.
// Ported verbatim from the vanilla UI's targetSize().
export function targetSize(
	mode: ResizeMode,
	meta: FileMeta | null,
	ratioPct: number,
	dimW: number,
	dimH: number,
): FileMeta | null {
	if (!meta) return null;
	if (mode === "ratio") {
		const r = (ratioPct || 100) / 100;
		return {
			w: Math.max(1, Math.round(meta.w * r)),
			h: Math.max(1, Math.round(meta.h * r)),
		};
	}
	if (mode === "dims") {
		return {
			w: Math.max(1, dimW || meta.w),
			h: Math.max(1, dimH || meta.h),
		};
	}
	return null;
}

// Flood-fill from the borders, turning background-colored pixels transparent.
// Edge-connected only, so same-colored regions inside the subject (e.g. white
// eyes on a white bg) are preserved. bg color = average of the 4 corners.
// Ported verbatim from the vanilla UI's keyOutBackground().
export function keyOutBackground(imageData: ImageData, tol: number): ImageData {
	const { data, width, height } = imageData;
	const corners = [
		[0, 0],
		[width - 1, 0],
		[0, height - 1],
		[width - 1, height - 1],
	];
	let r = 0,
		g = 0,
		b = 0;
	for (const [x, y] of corners) {
		const i = (y * width + x) * 4;
		r += data[i];
		g += data[i + 1];
		b += data[i + 2];
	}
	r /= 4;
	g /= 4;
	b /= 4;
	const matches = (i: number) =>
		Math.abs(data[i] - r) <= tol &&
		Math.abs(data[i + 1] - g) <= tol &&
		Math.abs(data[i + 2] - b) <= tol;

	const seen = new Uint8Array(width * height);
	const stack: number[] = [];
	for (let x = 0; x < width; x++) {
		stack.push(x, 0, x, height - 1);
	}
	for (let y = 0; y < height; y++) {
		stack.push(0, y, width - 1, y);
	}
	while (stack.length) {
		const y = stack.pop()!;
		const x = stack.pop()!;
		if (x < 0 || y < 0 || x >= width || y >= height) continue;
		const p = y * width + x;
		if (seen[p]) continue;
		seen[p] = 1;
		const i = p * 4;
		if (!matches(i)) continue;
		data[i + 3] = 0; // transparent
		stack.push(x + 1, y, x - 1, y, x, y + 1, x, y - 1);
	}
	return imageData;
}

// Draw the image onto a canvas (resized if target given), optionally key out the
// background, and return a PNG blob + final dimensions. Returns null when no
// canvas step is needed (use the original file as-is).
// Ported verbatim from the vanilla UI's prepareInput().
export async function prepareInput(
	file: File,
	meta: FileMeta | null,
	target: FileMeta | null,
	transparent: boolean,
): Promise<PreparedInput | null> {
	const resize = !!(
		target &&
		meta &&
		(target.w !== meta.w || target.h !== meta.h)
	);
	if (!resize && !transparent) return null;
	const w = target ? target.w : meta!.w;
	const h = target ? target.h : meta!.h;
	const bitmap = await createImageBitmap(file);
	const canvas = document.createElement("canvas");
	canvas.width = w;
	canvas.height = h;
	const ctx = canvas.getContext("2d")!;
	ctx.drawImage(bitmap, 0, 0, w, h);
	if (transparent) {
		const id = ctx.getImageData(0, 0, w, h);
		keyOutBackground(id, 32);
		ctx.putImageData(id, 0, 0);
	}
	const blob = await new Promise<Blob | null>((resolve) =>
		canvas.toBlob(resolve, "image/png"),
	);
	if (!blob) return null;
	return { blob, w, h, resized: resize, transparent };
}

// Derive extra metrics from the SVG text (viewBox + <path> count).
export function svgStats(svg: string): { viewBox?: string; paths: number } {
	const viewBox = (svg.match(/viewBox="([^"]+)"/) || [])[1];
	const paths = (svg.match(/<path\b/g) || []).length;
	return { viewBox, paths };
}
