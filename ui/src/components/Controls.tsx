import type { FileMeta, Quality, ResizeMode } from "../types";
import { Segmented } from "./Segmented";
import { LockIcon, PlayIcon } from "./Icons";

interface ControlsProps {
	meta: FileMeta | null;
	quality: Quality;
	setQuality: (q: Quality) => void;
	resizeMode: ResizeMode;
	setResizeMode: (m: ResizeMode) => void;
	ratioPct: number;
	setRatioPct: (n: number) => void;
	dimW: number;
	dimH: number;
	setDimW: (n: number) => void;
	setDimH: (n: number) => void;
	locked: boolean;
	setLocked: (b: boolean) => void;
	transparent: boolean;
	setTransparent: (b: boolean) => void;
	canRun: boolean;
	onRun: () => void;
}

// The left-panel option controls: quality preset, resize mode (+ contextual
// sub-controls with an aspect lock), transparent-background toggle, and the
// primary Trace action.
export function Controls({
	meta,
	quality,
	setQuality,
	resizeMode,
	setResizeMode,
	ratioPct,
	setRatioPct,
	dimW,
	dimH,
	setDimW,
	setDimH,
	locked,
	setLocked,
	transparent,
	setTransparent,
	canRun,
	onRun,
}: ControlsProps) {
	// Live px hint for ratio mode (falls back to raw % when no image dims yet).
	const ratioOut = meta
		? `→ ${Math.max(1, Math.round((meta.w * (ratioPct || 100)) / 100))}×${Math.max(1, Math.round((meta.h * (ratioPct || 100)) / 100))}px`
		: "";

	// Editing one dimension recomputes the other when the aspect ratio is locked.
	const onDimW = (v: number) => {
		setDimW(v);
		if (locked && meta && v) setDimH(Math.max(1, Math.round((v * meta.h) / meta.w)));
	};
	const onDimH = (v: number) => {
		setDimH(v);
		if (locked && meta && v) setDimW(Math.max(1, Math.round((v * meta.w) / meta.h)));
	};

	return (
		<>
			{/* QUALITY */}
			<div className="field">
				<label className="title">Quality</label>
				<Segmented<Quality>
					ariaLabel="Quality preset"
					value={quality}
					onChange={setQuality}
					options={[
						{ value: "faithful", label: "Faithful", sub: "exact · big" },
						{ value: "balanced", label: "Balanced", sub: "general" },
						{ value: "small", label: "Small", sub: "icons · ~5× smaller" },
					]}
				/>
			</div>

			{/* RESIZE */}
			<div className="field">
				<label className="title">Resize</label>
				<Segmented<ResizeMode>
					ariaLabel="Resize mode"
					value={resizeMode}
					onChange={setResizeMode}
					options={[
						{ value: "none", label: "Original" },
						{ value: "ratio", label: "By ratio" },
						{ value: "dims", label: "By size" },
					]}
				/>

				{resizeMode === "ratio" && (
					<div className="dims" style={{ marginTop: 12 }}>
						<input
							className="num wide"
							type="number"
							min={1}
							max={400}
							value={ratioPct}
							onChange={(e) => setRatioPct(Number(e.target.value))}
						/>
						<span className="unit">%</span>
						<span className="resolved">{ratioOut}</span>
					</div>
				)}

				{resizeMode === "dims" && (
					<div className="dims" style={{ marginTop: 12 }}>
						<input
							className="num wide"
							type="number"
							min={1}
							value={dimW || ""}
							onChange={(e) => onDimW(Number(e.target.value))}
						/>
						<span className="times">×</span>
						<input
							className="num wide"
							type="number"
							min={1}
							value={dimH || ""}
							onChange={(e) => onDimH(Number(e.target.value))}
						/>
						<button
							type="button"
							className="lock"
							aria-pressed={locked}
							title={
								locked
									? "Aspect ratio locked — click to unlock"
									: "Aspect ratio unlocked — dimensions independent"
							}
							onClick={() => setLocked(!locked)}
						>
							<LockIcon />
						</button>
						<span className="unit">px</span>
					</div>
				)}
			</div>

			{/* BACKGROUND */}
			<div className="field">
				<label className="title">Background</label>
				<div className="switch-row">
					<div className="checker-mini checker" />
					<div className="st">
						<div className="lbl">Make transparent</div>
						<div className="desc">Flood-key the edge background before tracing.</div>
					</div>
					<button
						type="button"
						className="switch"
						aria-pressed={transparent}
						aria-label="Transparent background"
						onClick={() => setTransparent(!transparent)}
					/>
				</div>
			</div>

			{/* PRIMARY ACTION */}
			<div className="field">
				<button type="button" className="btn primary block" disabled={!canRun} onClick={onRun}>
					<PlayIcon />
					Trace → SVG
				</button>
				<div className="help">
					Vector output scales to any size with no blur. Larger images take longer.
				</div>
			</div>
		</>
	);
}
