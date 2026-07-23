// Shared types for the img2svg tracer UI.

export type Quality = "faithful" | "balanced" | "small";
export type ResizeMode = "none" | "ratio" | "dims";

// The output panel is a small state machine. `compare` is a view mode layered
// on top of `result`, tracked separately (see App).
export type OutputState = "empty" | "loading" | "result" | "error";

export interface FileMeta {
	w: number;
	h: number;
}

// A picked source image plus its derived metadata and a preview object URL.
export interface PickedFile {
	file: File;
	meta: FileMeta | null;
	thumbUrl: string;
}

// Metrics derived from a successful trace, surfaced in the log + stat cards.
export interface TraceMetrics {
	name: string; // output filename, e.g. sprout.svg
	inBytes: number; // size of the bytes actually sent to the tracer
	outBytes: number; // size of the returned SVG
	paths: number; // <path> count in the SVG
	viewBox?: string; // viewBox attribute, if present
	ratio: number; // inBytes / outBytes (>1 = output smaller)
	ms: number; // wall-clock duration
	usedW: number | null; // dimensions of the traced input
	usedH: number | null;
	quality: Quality;
}

export type LogKind = "info" | "ok" | "err" | "warn" | "metric" | "";

export interface LogLine {
	id: number;
	ts: string;
	msg: string;
	kind: LogKind;
	indent?: boolean;
}
