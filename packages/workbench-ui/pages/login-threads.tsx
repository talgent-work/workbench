// Abstract threads behind the sign-in: streams of work converging on the
// centre and fanning out again, with a few lights travelling along them. They
// echo the product's collaboration chain; purely decorative.
const centre = { x: 800, y: 470 };

const streams = [
  { from: 90, to: 160, duration: 11, delay: -1 },
  { from: 250, to: 330, duration: 13, delay: -6 },
  { from: 420, to: 560, duration: 9, delay: -3 },
  { from: 640, to: 760, duration: 12, delay: -8 },
  { from: 840, to: 900, duration: 10, delay: -4 },
  { from: 980, to: 60, duration: 14, delay: -10 },
];

function streamPath(from: number, to: number) {
  return `M -60 ${from} C 330 ${from}, 520 ${centre.y}, ${centre.x} ${centre.y} S 1270 ${to}, 1660 ${to}`;
}

export function LoginThreads() {
  return (
    <svg className="si-threads" viewBox="0 0 1600 1000" preserveAspectRatio="xMidYMid slice" aria-hidden="true" focusable="false">
      <defs>
        <linearGradient id="si-thread" x1="0" y1="0" x2="1600" y2="0" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#6a4cf5" stopOpacity="0" />
          <stop offset=".3" stopColor="#6a4cf5" stopOpacity=".55" />
          <stop offset=".7" stopColor="#8f7bff" stopOpacity=".55" />
          <stop offset="1" stopColor="#f0a43a" stopOpacity="0" />
        </linearGradient>
        <linearGradient id="si-pulse" x1="0" y1="0" x2="1600" y2="0" gradientUnits="userSpaceOnUse">
          <stop offset="0" stopColor="#8f7bff" />
          <stop offset=".6" stopColor="#c59bff" />
          <stop offset="1" stopColor="#f0a43a" />
        </linearGradient>
      </defs>
      {streams.map((stream) => {
        const d = streamPath(stream.from, stream.to);
        return (
          <g key={stream.from}>
            <path className="si-thread" d={d} />
            <path
              className="si-thread-pulse"
              d={d}
              pathLength={1}
              style={{ animationDuration: `${stream.duration}s`, animationDelay: `${stream.delay}s` }}
            />
          </g>
        );
      })}
    </svg>
  );
}
