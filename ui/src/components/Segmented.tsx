// Reusable segmented control. Renders a row of buttons where exactly one is
// pressed (aria-pressed="true"), matching the mock's `.segmented.full`.

export interface SegmentedOption<T extends string> {
	value: T;
	label: string;
	sub?: string;
}

interface SegmentedProps<T extends string> {
	options: SegmentedOption<T>[];
	value: T;
	onChange: (value: T) => void;
	ariaLabel: string;
}

export function Segmented<T extends string>({
	options,
	value,
	onChange,
	ariaLabel,
}: SegmentedProps<T>) {
	return (
		<div className="segmented full" role="group" aria-label={ariaLabel}>
			{options.map((opt) => (
				<button
					key={opt.value}
					type="button"
					aria-pressed={value === opt.value}
					onClick={() => onChange(opt.value)}
				>
					{opt.label}
					{opt.sub && <span className="sub">{opt.sub}</span>}
				</button>
			))}
		</div>
	);
}
