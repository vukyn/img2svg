import type { TraceMetrics } from "../types";
import { human } from "../lib/format";

// Visual surfacing of the trace metrics as four cards. The size-ratio card goes
// green when the SVG is smaller than the source bytes.
export function StatCards({ metrics }: { metrics: TraceMetrics }) {
	const smaller = metrics.ratio >= 1;
	const ratioText = smaller
		? `${metrics.ratio.toFixed(1)}×`
		: `${(1 / metrics.ratio).toFixed(1)}×`;

	const [inVal, inUnit] = human(metrics.inBytes).split(" ");
	const [outVal, outUnit] = human(metrics.outBytes).split(" ");

	return (
		<div className="stats">
			<div className="stat">
				<div className="k">Input</div>
				<div className="v">
					{inVal}
					<small> {inUnit}</small>
				</div>
			</div>
			<div className="stat">
				<div className="k">Output</div>
				<div className="v">
					{outVal}
					<small> {outUnit}</small>
				</div>
			</div>
			<div className={`stat${smaller ? " good" : ""}`}>
				<div className="k">Size ratio</div>
				<div className="v">
					{ratioText}
					<small> {smaller ? "smaller" : "larger"}</small>
				</div>
			</div>
			<div className="stat">
				<div className="k">Paths</div>
				<div className="v">{metrics.paths}</div>
			</div>
		</div>
	);
}
