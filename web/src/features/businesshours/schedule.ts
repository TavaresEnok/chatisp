export type HoursInterval = { day: number; start: number; end: number };

const DAYS = ["Domingo", "Segunda", "Terça", "Quarta", "Quinta", "Sexta", "Sábado"] as const;

export function dayLabel(day: number): string {
  return DAYS[day] ?? "Dia inválido";
}

export function intervalError(intervals: readonly HoursInterval[]): string | null {
  if (intervals.length > 112) return "Há intervalos demais.";
  const byDay = new Map<number, HoursInterval[]>();
  for (const item of intervals) {
    if (!Number.isInteger(item.day) || item.day < 0 || item.day > 6) return "Dia inválido.";
    if (!Number.isInteger(item.start) || !Number.isInteger(item.end)) return "Use minutos inteiros.";
    if (item.start < 0 || item.start > 1439 || item.end < 1 || item.end > 1440 || item.start >= item.end) return "Intervalo inválido.";
    const list = byDay.get(item.day) ?? [];
    list.push(item);
    byDay.set(item.day, list);
  }
  for (const list of byDay.values()) {
    const sorted = [...list].sort((left, right) => left.start - right.start || left.end - right.end);
    for (let index = 1; index < sorted.length; index += 1) {
      const previous = sorted[index - 1];
      const current = sorted[index];
      if (previous && current && current.start < previous.end) return "Intervalos sobrepostos no mesmo dia.";
    }
  }
  return null;
}

export function noticeError(value: string): string | null {
  if (value.includes("\u0000") || [...value].length > 4000) return "O texto passa de 4000 caracteres.";
  return null;
}

export function configError(input: { intervals: readonly HoursInterval[]; welcome: string; outsideHours: string; nobodyAvailable: string }): string | null {
  return intervalError(input.intervals) ?? noticeError(input.welcome) ?? noticeError(input.outsideHours) ?? noticeError(input.nobodyAvailable);
}

export function minutesLabel(minutes: number): string {
  const hour = Math.floor(minutes / 60);
  const minute = minutes % 60;
  return `${String(hour).padStart(2, "0")}:${String(minute).padStart(2, "0")}`;
}
