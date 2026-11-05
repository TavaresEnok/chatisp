const weekdays = ["domingo", "segunda", "terça", "quarta", "quinta", "sexta", "sábado"] as const;
const hours = Array.from({ length: 24 }, (_, hour) => hour);

type HeatCell = { weekday: number; hour: number; cycles: number };

function cellCycles(cells: HeatCell[]): Map<string, number> {
  const known = new Map<string, number>();
  for (const cell of cells) {
    if (!Number.isInteger(cell.weekday) || cell.weekday < 0 || cell.weekday > 6) continue;
    if (!Number.isInteger(cell.hour) || cell.hour < 0 || cell.hour > 23) continue;
    if (!Number.isFinite(cell.cycles)) continue;
    known.set(`${cell.weekday}-${cell.hour}`, cell.cycles);
  }
  return known;
}

function cellLabel(day: string, hour: number, cycles: number | undefined): string {
  if (cycles === undefined) return `${day}, ${hour} horas, sem registro`;
  if (cycles === 0) return `${day}, ${hour} horas, zero ciclos`;
  const noun = cycles === 1 ? "ciclo" : "ciclos";
  return `${day}, ${hour} horas, ${cycles} ${noun}`;
}

export function Heatmap({ timezone, cells }: { timezone: string; cells: HeatCell[] }) {
  const known = cellCycles(cells);
  const zone = timezone.trim().length > 0 ? timezone : "fuso não informado";
  return <section aria-label="Mapa de calor">
    <h2>Mapa de calor</h2>
    <p>
      {known.size === 0
        ? "O cálculo não devolveu células. Nenhuma hora deste mapa vira zero por conta própria."
        : `O cálculo devolveu ${known.size} ${known.size === 1 ? "hora" : "horas"} no fuso ${zone}.`}
    </p>
    <ul aria-label="Legenda do mapa de calor">
      <li>Número: ciclos iniciados nessa hora, no fuso {zone}, como o cálculo devolveu.</li>
      <li>Zero: o cálculo devolveu 0 ciclos. Não é ausência de resposta.</li>
      <li>Sem registro: a resposta não trouxe esta hora. O painel não transforma isso em zero.</li>
    </ul>
    <div className="overflow-x-auto" role="region" aria-label="Horas por dia, rolagem horizontal" tabIndex={0}>
      <table className="border-collapse text-sm">
        <caption>Mapa de calor no fuso {zone}. Sete dias por vinte e quatro horas. Domingo é o dia 0 do cálculo.</caption>
        <thead>
          <tr>
            <th scope="col">Dia</th>
            {hours.map((hour) => <th key={hour} scope="col" abbr={`${hour} horas`}>{hour}</th>)}
          </tr>
        </thead>
        <tbody>
          {weekdays.map((day, weekday) => <tr key={day}>
            <th scope="row">{day}</th>
            {hours.map((hour) => {
              const cycles = known.get(`${weekday}-${hour}`);
              const present = cycles !== undefined && cycles > 0;
              return <td
                key={hour}
                aria-label={cellLabel(day, hour, cycles)}
                className={cycles === undefined ? "border border-dashed px-1 text-center" : "border px-1 text-center"}
                style={present ? { background: "rgb(14 116 144 / 0.25)" } : undefined}
              >{cycles === undefined ? "sem registro" : String(cycles)}</td>;
            })}
          </tr>)}
        </tbody>
      </table>
    </div>
  </section>;
}
