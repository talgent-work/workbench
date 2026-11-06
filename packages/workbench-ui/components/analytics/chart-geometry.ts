export type TrendSeries<TPoint> = { key: keyof TPoint; label: string; color: string; defaultHidden?: boolean };

export function trendPath<TPoint extends { label: string }>(points: TPoint[], key: keyof TPoint, maxValue: number) {
  if (points.length === 1) {
    const y = pointY(numericValue(points[0], key), maxValue).toFixed(2);
    return `M 0.00 ${y} L 100.00 ${y}`;
  }
  return points
    .map((point) => {
      const coords = pointCoords(points, point, key, maxValue);
      return `${points.indexOf(point) === 0 ? "M" : "L"} ${coords.x.toFixed(2)} ${coords.y.toFixed(2)}`;
    })
    .join(" ");
}

export function pointCoords<TPoint extends { label: string }>(points: TPoint[], point: TPoint, key: keyof TPoint, maxValue: number) {
  const index = points.indexOf(point);
  const x = pointX(points.length, index);
  const y = pointY(numericValue(point, key), maxValue);
  return { x, y };
}

export function numericValue<TPoint>(point: TPoint, key: keyof TPoint): number {
  const value = point[key];
  return typeof value === "number" && Number.isFinite(value) ? value : 0;
}

export function pointX(pointCount: number, index: number) {
  return pointCount <= 1 ? 50 : (index / (pointCount - 1)) * 100;
}

export function pointY(value: number, maxValue: number) {
  return 88 - (value / maxValue) * 72;
}

export function niceAxisMax(value: number) {
  const safeValue = Number.isFinite(value) && value > 0 ? value : 1;
  const step = niceTickStep(safeValue / 4);
  return Math.max(step, Math.ceil(safeValue / step) * step);
}

export function yTickValues(maxValue: number) {
  const step = niceTickStep(maxValue / 4);
  const ticks: number[] = [];
  for (let value = maxValue; value >= 0; value -= step) {
    ticks.push(Number(value.toFixed(6)));
  }
  if (ticks[ticks.length - 1] !== 0) ticks.push(0);
  return ticks;
}

function niceTickStep(rawStep: number) {
  const safeStep = Number.isFinite(rawStep) && rawStep > 0 ? rawStep : 1;
  const magnitude = 10 ** Math.floor(Math.log10(safeStep));
  const normalized = safeStep / magnitude;
  const niceNormalized = normalized <= 1 ? 1 : normalized <= 2 ? 2 : normalized <= 5 ? 5 : 10;
  return Math.max(1, niceNormalized * magnitude);
}

export function stackedAreaPath<TPoint extends { label: string }>(
  points: TPoint[],
  series: readonly TrendSeries<TPoint>[],
  seriesIndex: number,
  maxValue: number,
) {
  if (points.length === 1) {
    const point = points[0];
    const top = pointY(cumulativeValue(point, series, seriesIndex), maxValue).toFixed(2);
    const bottom = pointY(cumulativeValue(point, series, seriesIndex - 1), maxValue).toFixed(2);
    return `M 0.00 ${top} L 100.00 ${top} L 100.00 ${bottom} L 0.00 ${bottom} Z`;
  }
  const top = points.map((point, pointIndex) => {
    const value = cumulativeValue(point, series, seriesIndex);
    return `${pointIndex === 0 ? "M" : "L"} ${pointX(points.length, pointIndex).toFixed(2)} ${pointY(value, maxValue).toFixed(2)}`;
  });
  const bottom = [...points].reverse().map((point, reverseIndex) => {
    const originalIndex = points.length - 1 - reverseIndex;
    const value = cumulativeValue(point, series, seriesIndex - 1);
    return `L ${pointX(points.length, originalIndex).toFixed(2)} ${pointY(value, maxValue).toFixed(2)}`;
  });
  return [...top, ...bottom, "Z"].join(" ");
}

export function cumulativeValue<TPoint>(point: TPoint, series: readonly TrendSeries<TPoint>[], throughIndex: number) {
  if (throughIndex < 0) return 0;
  return series.slice(0, throughIndex + 1).reduce((sum, item) => sum + numericValue(point, item.key), 0);
}

// Labelled buckets for the time axis: at most `max`, a whole number of buckets
// apart, so the labels are evenly spaced (the last may fall short of the end).
export function xTickIndices(pointCount: number, max = 5) {
  if (pointCount <= 0) return [];
  const step = Math.max(1, Math.ceil((pointCount - 1) / (max - 1)));
  return Array.from({ length: Math.floor((pointCount - 1) / step) + 1 }, (_, index) => index * step);
}
