import { useCallback, useEffect, useRef, useState } from "react";

import { CompareHandleIcon } from "./Icons";

interface CompareSliderProps {
	rasterUrl: string; // the prepared source bitmap actually sent to the tracer
	svgUrl: string; // the traced SVG, as an object URL
}

// Draggable split view: raster on the left, traced vector on the right, revealed
// by a vertical handle. The vector layer is clipped to the handle position.
export function CompareSlider({ rasterUrl, svgUrl }: CompareSliderProps) {
	const wrapRef = useRef<HTMLDivElement>(null);
	const [pct, setPct] = useState(50);
	const dragging = useRef(false);

	const setFromClientX = useCallback((clientX: number) => {
		const wrap = wrapRef.current;
		if (!wrap) return;
		const r = wrap.getBoundingClientRect();
		const next = ((clientX - r.left) / r.width) * 100;
		setPct(Math.max(0, Math.min(100, next)));
	}, []);

	useEffect(() => {
		const onMove = (e: MouseEvent) => {
			if (dragging.current) setFromClientX(e.clientX);
		};
		const onTouch = (e: TouchEvent) => {
			if (dragging.current && e.touches[0]) setFromClientX(e.touches[0].clientX);
		};
		const stop = () => {
			dragging.current = false;
		};
		window.addEventListener("mousemove", onMove);
		window.addEventListener("touchmove", onTouch, { passive: true });
		window.addEventListener("mouseup", stop);
		window.addEventListener("touchend", stop);
		return () => {
			window.removeEventListener("mousemove", onMove);
			window.removeEventListener("touchmove", onTouch);
			window.removeEventListener("mouseup", stop);
			window.removeEventListener("touchend", stop);
		};
	}, [setFromClientX]);

	return (
		<div className="compare-wrap" ref={wrapRef} style={{ display: "block" }}>
			<div className="layer raster">
				<img src={rasterUrl} alt="raster source" />
			</div>
			{/*
			  * The clip is on this wrapper, never on the SVG itself, so the vector can
			  * be an <img> like the raster beside it — which is what keeps the traced
			  * markup out of this document entirely. It also makes the two layers
			  * measure identically, since `.stage img` in index.css now sizes both.
			  */}
			<div className="layer vector" style={{ clipPath: `inset(0 0 0 ${pct}%)` }}>
				<img src={svgUrl} alt="traced vector output" />
			</div>
			<div className="compare-tag l">RASTER</div>
			<div className="compare-tag r">SVG</div>
			<div
				className="compare-handle"
				style={{ left: `${pct}%` }}
				onMouseDown={() => {
					dragging.current = true;
				}}
				onTouchStart={() => {
					dragging.current = true;
				}}
			>
				<CompareHandleIcon />
			</div>
		</div>
	);
}
