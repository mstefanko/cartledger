interface SparklineProps {
  data: number[]
  highlights?: boolean[]  // optional: true = render green dot at this index
  width?: number
  height?: number
  color?: string
}

function Sparkline({ data, highlights, width = 80, height = 24, color = '#7132f5' }: SparklineProps) {
  if (data.length < 2) return null

  const min = Math.min(...data)
  const max = Math.max(...data)
  const range = max - min || 1

  const coords = data.map((v, i) => ({
    x: (i / (data.length - 1)) * width,
    y: height - ((v - min) / range) * (height - 2) - 1,
  }))

  const points = coords.map((c) => `${c.x},${c.y}`).join(' ')

  return (
    <svg width={width} height={height} className="inline-block align-middle">
      <polyline fill="none" stroke={color} strokeWidth={1.5} points={points} />
      {highlights && highlights.map((isHighlighted, i) => {
        const coord = coords[i]
        if (!isHighlighted || !coord) return null
        return <circle key={i} cx={coord.x} cy={coord.y} r={2} fill="#16a34a" />
      })}
    </svg>
  )
}

export { Sparkline }
export type { SparklineProps }
