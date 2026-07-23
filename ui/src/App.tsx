import { useCallback, useEffect, useRef, useState } from "react";

import type {
	LogKind,
	LogLine,
	OutputState,
	PickedFile,
	Quality,
	ResizeMode,
	TraceMetrics,
} from "./types";
import { human, timestamp } from "./lib/format";
import { prepareInput, svgStats, targetSize } from "./lib/image";
import { Controls } from "./components/Controls";
import { LogPanel } from "./components/LogPanel";
import { Lightbox } from "./components/Lightbox";
import { OutputPanel } from "./components/OutputPanel";
import { UploadZone } from "./components/UploadZone";
import { LogoMark } from "./components/Icons";

export default function App() {
	// ── source + options ──
	const [picked, setPicked] = useState<PickedFile | null>(null);
	const [quality, setQuality] = useState<Quality>("faithful");
	const [resizeMode, setResizeMode] = useState<ResizeMode>("none");
	const [ratioPct, setRatioPct] = useState(100);
	const [dimW, setDimW] = useState(0);
	const [dimH, setDimH] = useState(0);
	const [locked, setLocked] = useState(true);
	const [transparent, setTransparent] = useState(false);

	// ── output ──
	const [outputState, setOutputState] = useState<OutputState>("empty");
	const [compareMode, setCompareMode] = useState(false);
	const [svgText, setSvgText] = useState<string | null>(null);
	const [rasterUrl, setRasterUrl] = useState<string | null>(null);
	const [metrics, setMetrics] = useState<TraceMetrics | null>(null);
	const [errorCode, setErrorCode] = useState<string | null>(null);
	const [lightboxOpen, setLightboxOpen] = useState(false);

	// ── log ──
	const [logLines, setLogLines] = useState<LogLine[]>([
		{ id: 0, ts: timestamp(), msg: "ready. drop an image to start.", kind: "info" },
	]);
	const logId = useRef(1);

	// Object URLs are freed on replacement so previews don't leak between picks.
	const rasterUrlRef = useRef<string | null>(null);
	const thumbUrlRef = useRef<string | null>(null);

	const addLog = useCallback(
		(msg: string, kind: LogKind = "", indent = false) => {
			setLogLines((prev) => [
				...prev,
				{ id: logId.current++, ts: timestamp(), msg, kind, indent },
			]);
		},
		[],
	);

	const clearLog = () => {
		setLogLines([{ id: logId.current++, ts: timestamp(), msg: "log cleared.", kind: "info" }]);
	};

	const revokeRaster = () => {
		if (rasterUrlRef.current) {
			URL.revokeObjectURL(rasterUrlRef.current);
			rasterUrlRef.current = null;
		}
	};

	const resetOutput = useCallback(() => {
		setOutputState("empty");
		setCompareMode(false);
		setSvgText(null);
		setMetrics(null);
		setErrorCode(null);
		revokeRaster();
		setRasterUrl(null);
	}, []);

	// Pick (or paste/drop) a source image: build a preview, read natural dims, and
	// prime the resize dimension inputs.
	const setFile = useCallback(
		(file: File) => {
			if (thumbUrlRef.current) URL.revokeObjectURL(thumbUrlRef.current);
			const thumbUrl = URL.createObjectURL(file);
			thumbUrlRef.current = thumbUrl;
			setPicked({ file, meta: null, thumbUrl });
			resetOutput();

			const img = new Image();
			img.onload = () => {
				const meta = { w: img.naturalWidth, h: img.naturalHeight };
				setPicked((prev) => (prev && prev.file === file ? { ...prev, meta } : prev));
				setDimW(meta.w);
				setDimH(meta.h);
				addLog(
					`selected ${file.name} — ${file.type || "?"} · ${meta.w}×${meta.h}px · ${human(file.size)}`,
					"info",
				);
			};
			img.onerror = () => {
				addLog(`selected ${file.name} · ${human(file.size)}`, "info");
			};
			img.src = thumbUrl;
		},
		[addLog, resetOutput],
	);

	const onReplace = () => {
		if (thumbUrlRef.current) {
			URL.revokeObjectURL(thumbUrlRef.current);
			thumbUrlRef.current = null;
		}
		setPicked(null);
		resetOutput();
	};

	// Trace: prepare the input (optional resize + optional transparent bg), POST
	// to /api/trace, then render the SVG and surface metrics.
	const run = useCallback(async () => {
		if (!picked) return;
		const { file, meta } = picked;
		setOutputState("loading");
		setCompareMode(false);
		addLog(`tracing ${file.name} @ ${quality} …`, "info");
		const t0 = performance.now();

		let uploadBlob: Blob = file;
		let usedW = meta ? meta.w : null;
		let usedH = meta ? meta.h : null;

		try {
			const target = targetSize(resizeMode, meta, ratioPct, dimW, dimH);
			const prepped = await prepareInput(file, meta, target, transparent);
			if (prepped) {
				uploadBlob = prepped.blob;
				usedW = prepped.w;
				usedH = prepped.h;
				const notes = [
					prepped.resized ? `resized → ${prepped.w}×${prepped.h}` : null,
					prepped.transparent ? "bg → transparent" : null,
				]
					.filter(Boolean)
					.join(" · ");
				addLog(`prepared input (${notes}) …`, "info");
			}

			const fd = new FormData();
			fd.append("image", uploadBlob, file.name);
			fd.append("quality", quality);

			const res = await fetch("/api/trace", { method: "POST", body: fd });
			if (!res.ok) {
				const msg = (await res.text()).trim();
				setErrorCode(`${res.status} · ${msg}`);
				setOutputState("error");
				addLog(`error: ${msg}`, "err");
				return;
			}

			const svg = await res.text();
			const ms = Math.round(performance.now() - t0);
			const inBytes = uploadBlob.size;
			const outBytes = new Blob([svg]).size;
			const { viewBox, paths } = svgStats(svg);
			const ratio = inBytes / outBytes;
			const name = file.name.replace(/\.[^.]+$/, "") + ".svg";

			// Fresh raster URL for the compare view = the exact bytes we sent.
			revokeRaster();
			const newRasterUrl = URL.createObjectURL(uploadBlob);
			rasterUrlRef.current = newRasterUrl;
			setRasterUrl(newRasterUrl);

			setSvgText(svg);
			setMetrics({
				name,
				inBytes,
				outBytes,
				paths,
				viewBox,
				ratio,
				ms,
				usedW,
				usedH,
				quality,
			});
			setOutputState("result");

			addLog(`done → ${name}`, "ok");
			addLog(
				`input:  ${usedW && usedH ? `${usedW}×${usedH}px · ` : ""}${human(inBytes)} (traced)`,
				"metric",
				true,
			);
			addLog(
				`output: ${human(outBytes)} · ${paths} paths${viewBox ? ` · viewBox ${viewBox}` : ""}`,
				"metric",
				true,
			);
			const sizeNote =
				ratio >= 1 ? `${ratio.toFixed(1)}× smaller` : `${(1 / ratio).toFixed(1)}× larger`;
			addLog(
				`size:   ${sizeNote} than source · ${ms} ms · quality=${quality}`,
				ratio >= 1 ? "ok" : "info",
				true,
			);
		} catch (err) {
			const message = err instanceof Error ? err.message : String(err);
			setErrorCode(`request failed · ${message}`);
			setOutputState("error");
			addLog(`request failed: ${message}`, "err");
		}
	}, [picked, quality, resizeMode, ratioPct, dimW, dimH, transparent, addLog]);

	const canRun = !!picked && outputState !== "loading";

	// Copy the raw SVG markup to the clipboard.
	const onCopy = () => {
		if (svgText) navigator.clipboard?.writeText(svgText).catch(() => {});
	};

	// Download the traced SVG as a file.
	const onDownload = () => {
		if (!svgText || !metrics) return;
		const url = URL.createObjectURL(new Blob([svgText], { type: "image/svg+xml" }));
		const a = document.createElement("a");
		a.href = url;
		a.download = metrics.name;
		a.click();
		URL.revokeObjectURL(url);
		addLog(`downloaded ${metrics.name}`, "info");
	};

	// ── global shortcuts: ⌘V paste to upload, ↵ to trace ──
	useEffect(() => {
		const onPaste = (e: ClipboardEvent) => {
			const items = e.clipboardData?.items;
			if (!items) return;
			for (const item of items) {
				if (item.type.startsWith("image/")) {
					const file = item.getAsFile();
					if (file) {
						setFile(file);
						break;
					}
				}
			}
		};
		window.addEventListener("paste", onPaste);
		return () => window.removeEventListener("paste", onPaste);
	}, [setFile]);

	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			if (e.key !== "Enter" || lightboxOpen) return;
			const tag = (e.target as HTMLElement | null)?.tagName;
			if (tag === "INPUT" || tag === "TEXTAREA") return;
			if (canRun) run();
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [canRun, run, lightboxOpen]);

	// Free any live object URLs on unmount.
	useEffect(
		() => () => {
			revokeRaster();
			if (thumbUrlRef.current) URL.revokeObjectURL(thumbUrlRef.current);
		},
		[],
	);

	return (
		<div className="wrap">
			<header>
				<div className="brand">
					<div className="logo">
						<LogoMark />
					</div>
					<div>
						<h1>
							img2<b>svg</b>
						</h1>
						<p className="tagline">
							Trace raster art, icons &amp; logos into crisp, infinitely-scalable SVG.
						</p>
					</div>
				</div>
				<div className="head-right">
					<span className="badge">
						<span className="dot" />
						engine ready
					</span>
					<span className="badge">python + vtracer</span>
					<span className="badge" title="Keyboard shortcuts">
						<span className="kbd">⌘V</span> paste &nbsp;·&nbsp; <span className="kbd">↵</span> trace
					</span>
				</div>
			</header>

			<div className="grid">
				{/* SOURCE + OPTIONS */}
				<section className="panel">
					<div className="panel-head">
						<span className="step">1</span>
						<h2>Source &amp; options</h2>
					</div>
					<div className="panel-body">
						<UploadZone picked={picked} onFile={setFile} onReplace={onReplace} />
						<Controls
							meta={picked?.meta ?? null}
							quality={quality}
							setQuality={setQuality}
							resizeMode={resizeMode}
							setResizeMode={setResizeMode}
							ratioPct={ratioPct}
							setRatioPct={setRatioPct}
							dimW={dimW}
							dimH={dimH}
							setDimW={setDimW}
							setDimH={setDimH}
							locked={locked}
							setLocked={setLocked}
							transparent={transparent}
							setTransparent={setTransparent}
							canRun={canRun}
							onRun={run}
						/>
					</div>
				</section>

				{/* OUTPUT */}
				<OutputPanel
					state={outputState}
					compareMode={compareMode}
					svgText={svgText}
					rasterUrl={rasterUrl}
					metrics={metrics}
					errorCode={errorCode}
					quality={quality}
					onToggleCompare={() => setCompareMode((v) => !v)}
					onExpand={() => setLightboxOpen(true)}
					onCopy={onCopy}
					onDownload={onDownload}
					onRetry={run}
				/>
			</div>

			<LogPanel lines={logLines} onClear={clearLog} />

			<p className="footnote">
				Vector output scales to any size without blur · powered by python + vtracer.
			</p>

			{lightboxOpen && svgText && (
				<Lightbox svgText={svgText} onClose={() => setLightboxOpen(false)} />
			)}
		</div>
	);
}
