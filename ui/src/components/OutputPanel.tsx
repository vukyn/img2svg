import { useEffect, useState } from "react";

import type { OutputState, Quality, TraceMetrics } from "../types";
import { human } from "../lib/format";
import { CompareSlider } from "./CompareSlider";
import { StatCards } from "./StatCards";
import {
	AlertIcon,
	CheckIcon,
	CompareIcon,
	CopyIcon,
	DownloadIcon,
	ExpandIcon,
	ImageIcon,
	RetryIcon,
} from "./Icons";

interface OutputPanelProps {
	state: OutputState;
	compareMode: boolean;
	// The traced SVG as an object URL, loaded through <img> rather than injected as
	// markup — see the result layer below.
	svgUrl: string | null;
	rasterUrl: string | null;
	metrics: TraceMetrics | null;
	errorCode: string | null;
	quality: Quality;
	onToggleCompare: () => void;
	onExpand: () => void;
	onCopy: () => void;
	onDownload: () => void;
	onRetry: () => void;
}

// Step 2: the output stage (result / compare / empty / loading / error), the
// action toolbar, and the metric stat cards.
export function OutputPanel({
	state,
	compareMode,
	svgUrl,
	rasterUrl,
	metrics,
	errorCode,
	quality,
	onToggleCompare,
	onExpand,
	onCopy,
	onDownload,
	onRetry,
}: OutputPanelProps) {
	const hasOutput = state === "result";
	const [copied, setCopied] = useState(false);

	useEffect(() => {
		if (!copied) return;
		const t = window.setTimeout(() => setCopied(false), 1300);
		return () => window.clearTimeout(t);
	}, [copied]);

	const filenameSuffix = metrics
		? ` · ${human(metrics.outBytes)} · ${metrics.ms} ms`
		: "";

	return (
		<section className="panel">
			<div className="panel-head">
				<span className="step">2</span>
				<h2>Output</h2>
				<div className="spacer" />
				<button
					type="button"
					className="btn ghost sm"
					disabled={!hasOutput || !rasterUrl}
					onClick={onToggleCompare}
				>
					<CompareIcon />
					Compare
				</button>
				<button type="button" className="btn ghost sm" disabled={!hasOutput} onClick={onExpand}>
					<ExpandIcon />
					Expand
				</button>
			</div>
			<div className="panel-body">
				<div className="stage checker">
					{/*
					  * Loaded as a document, not injected as markup. The bytes come out of
					  * a subprocess, and SVG injected into this DOM runs animation and
					  * event handlers (<animate onbegin>, <image onerror>) even though it
					  * cannot run a <script>. An <img> gets none of that: no scripting, no
					  * handlers, no reach into this page.
					  *
					  * Nothing is lost by it here. The layer is a plain flex box that
					  * centres whatever is inside, and `.stage svg, .stage img` in index.css
					  * already sizes both the same way.
					  */}
					{hasOutput && !compareMode && svgUrl && (
						<div className="result-layer">
							<img src={svgUrl} alt="traced vector output" />
						</div>
					)}

					{hasOutput && compareMode && svgUrl && rasterUrl && (
						<CompareSlider rasterUrl={rasterUrl} svgUrl={svgUrl} />
					)}

					{hasOutput && (
						<div className="stage-hint">scroll to zoom · click ⛶ for fullscreen</div>
					)}

					{state === "empty" && (
						<div className="empty-state">
							<ImageIcon />
							<div className="t">No output yet</div>
							<div className="s">
								Drop an image and hit <b>Trace → SVG</b> to see the vector here.
							</div>
						</div>
					)}

					{state === "loading" && (
						<div className="loading-state">
							<div className="spinner" />
							<div className="t">Tracing…</div>
							<div className="s">python + vtracer · quality={quality}</div>
							<div className="progress">
								<i />
							</div>
						</div>
					)}

					{state === "error" && (
						<div className="error-state">
							<div className="ic">
								<AlertIcon />
							</div>
							<div className="t">Trace failed</div>
							<div className="code">{errorCode}</div>
							<button type="button" className="btn sm" onClick={onRetry}>
								<RetryIcon />
								Retry
							</button>
						</div>
					)}
				</div>

				{hasOutput && (
					<>
						<div className="out-tools">
							<span className="filename">
								<b>{metrics?.name}</b>
								{filenameSuffix}
							</span>
							<div className="spacer" />
							<button
								type="button"
								className="btn sm"
								onClick={() => {
									onCopy();
									setCopied(true);
								}}
							>
								{copied ? <CheckIcon /> : <CopyIcon />}
								{copied ? "Copied" : "Copy SVG"}
							</button>
							<button type="button" className="btn sm primary" onClick={onDownload}>
								<DownloadIcon />
								Download .svg
							</button>
						</div>

						{metrics && <StatCards metrics={metrics} />}
					</>
				)}
			</div>
		</section>
	);
}
