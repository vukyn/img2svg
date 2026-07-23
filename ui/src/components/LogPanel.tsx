import { useEffect, useRef } from "react";

import type { LogLine } from "../types";
import { TrashIcon } from "./Icons";

interface LogPanelProps {
	lines: LogLine[];
	onClear: () => void;
}

// Timestamped, color-coded activity log (step 3). Auto-scrolls to the newest
// line as entries arrive.
export function LogPanel({ lines, onClear }: LogPanelProps) {
	const logRef = useRef<HTMLDivElement>(null);

	useEffect(() => {
		const el = logRef.current;
		if (el) el.scrollTop = el.scrollHeight;
	}, [lines]);

	return (
		<section className="panel" style={{ marginTop: 18 }}>
			<div className="panel-head">
				<span className="step">3</span>
				<h2>Log</h2>
				<div className="spacer" />
				<button type="button" className="btn ghost sm" onClick={onClear}>
					<TrashIcon />
					Clear
				</button>
			</div>
			<div className="panel-body">
				<div className="log" ref={logRef}>
					{lines.map((line) => (
						<span className="line" key={line.id}>
							{line.indent ? (
								<span className="metric">{"  "}</span>
							) : (
								<span className="ts">[{line.ts}] </span>
							)}
							<span className={line.kind}>{line.msg}</span>
						</span>
					))}
				</div>
			</div>
		</section>
	);
}
