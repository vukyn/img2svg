import { useCallback, useEffect, useRef, useState } from "react";

import { CloseIcon, FitIcon, MinusIcon, PlusIcon } from "./Icons";

interface LightboxProps {
	svgText: string;
	onClose: () => void;
}

const MIN_ZOOM = 0.25;
const MAX_ZOOM = 8;
const clampZoom = (z: number) => Math.max(MIN_ZOOM, Math.min(MAX_ZOOM, z));

// Fullscreen viewer for the traced SVG: scroll / +− to zoom, drag to pan, fit to
// reset, close via ✕ / backdrop / Esc.
export function Lightbox({ svgText, onClose }: LightboxProps) {
	const [zoom, setZoom] = useState(1);
	const pan = useRef({ x: 0, y: 0 });
	const [, force] = useState(0);
	const rerender = () => force((n) => n + 1);
	const panning = useRef(false);
	const start = useRef({ x: 0, y: 0 });
	const stageRef = useRef<HTMLDivElement>(null);

	const reset = useCallback(() => {
		setZoom(1);
		pan.current = { x: 0, y: 0 };
		rerender();
	}, []);

	// Keyboard: Esc closes, +/- zoom.
	useEffect(() => {
		const onKey = (e: KeyboardEvent) => {
			if (e.key === "Escape") onClose();
			else if (e.key === "+" || e.key === "=") setZoom((z) => clampZoom(z * 1.25));
			else if (e.key === "-") setZoom((z) => clampZoom(z / 1.25));
		};
		window.addEventListener("keydown", onKey);
		return () => window.removeEventListener("keydown", onKey);
	}, [onClose]);

	// Drag-to-pan tracked on window so the drag survives leaving the stage.
	useEffect(() => {
		const onMove = (e: MouseEvent) => {
			if (!panning.current) return;
			pan.current = { x: e.clientX - start.current.x, y: e.clientY - start.current.y };
			rerender();
		};
		const onUp = () => {
			panning.current = false;
			stageRef.current?.classList.remove("grabbing");
		};
		window.addEventListener("mousemove", onMove);
		window.addEventListener("mouseup", onUp);
		return () => {
			window.removeEventListener("mousemove", onMove);
			window.removeEventListener("mouseup", onUp);
		};
	}, []);

	const transform = `translate(${pan.current.x}px, ${pan.current.y}px) scale(${zoom})`;

	return (
		<div
			className="lightbox"
			onClick={(e) => {
				if (e.target === e.currentTarget) onClose();
			}}
		>
			<div className="lb-bar">
				<button
					type="button"
					className="icon-btn"
					aria-label="Zoom out"
					onClick={() => setZoom((z) => clampZoom(z / 1.25))}
				>
					<MinusIcon />
				</button>
				<span className="zoomval">{Math.round(zoom * 100)}%</span>
				<button
					type="button"
					className="icon-btn"
					aria-label="Zoom in"
					onClick={() => setZoom((z) => clampZoom(z * 1.25))}
				>
					<PlusIcon />
				</button>
				<button type="button" className="icon-btn" aria-label="Fit" onClick={reset}>
					<FitIcon />
				</button>
			</div>
			<button type="button" className="lb-close" aria-label="Close (Esc)" onClick={onClose}>
				<CloseIcon />
			</button>
			<div
				className="lb-stage checker"
				ref={stageRef}
				onWheel={(e) => {
					setZoom((z) => clampZoom(z * (e.deltaY < 0 ? 1.1 : 0.9)));
				}}
				onMouseDown={(e) => {
					panning.current = true;
					start.current = { x: e.clientX - pan.current.x, y: e.clientY - pan.current.y };
					stageRef.current?.classList.add("grabbing");
				}}
			>
				<div
					className="lb-canvas"
					style={{ transform }}
					dangerouslySetInnerHTML={{ __html: svgText }}
				/>
			</div>
			<div className="lb-hint">scroll or +/− to zoom · drag to pan · Esc to close</div>
		</div>
	);
}
