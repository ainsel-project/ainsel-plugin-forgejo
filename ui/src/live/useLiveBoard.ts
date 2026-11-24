// useLiveBoard owns the board's relationship with the plugin: the one fetch,
// the clock it was last made at, and the pause that holds the board on what it
// last showed. The page reads the state and draws; nothing about refreshing
// lives in the component.

import { useEffect, useState } from "react";
import type { HttpClient } from "@ainsel/plugin-sdk";
import type { Run, WorkResponse } from "../api";
import { flattenRuns, REFRESH_MS } from "./model";

export interface LiveBoard {
  runs: Run[] | null;
  error: string | null;
  updatedAt: number | null;
  paused: boolean;
  setPaused: (next: (value: boolean) => boolean) => void;
}

export function useLiveBoard(api: HttpClient): LiveBoard {
  const [runs, setRuns] = useState<Run[] | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [updatedAt, setUpdatedAt] = useState<number | null>(null);
  const [paused, setPaused] = useState(false);

  useEffect(() => {
    // Paused, there is no timer and no fetch: the board holds what it last showed
    // until the operator resumes, which fetches at once rather than waiting out
    // the rest of an interval.
    if (paused) return;
    let cancelled = false;
    // Runs come from the plugin's own work route, each carrying its agent's id
    // (ruling C3) — what identifies — and the display name the plugin resolved for
    // it — what a human reads.
    const load = () => {
      api
        .get("/work", { query: { since: "1d" } })
        .then((response) => {
          if (cancelled) return;
          setRuns(flattenRuns((response as WorkResponse).repos));
          setError(null);
          setUpdatedAt(Date.now());
        })
        .catch((err) => {
          if (!cancelled) setError(err.message);
        });
    };
    load();
    const timer = setInterval(load, REFRESH_MS);
    return () => {
      cancelled = true;
      clearInterval(timer);
    };
  }, [api, paused]);

  return { runs, error, updatedAt, paused, setPaused };
}
