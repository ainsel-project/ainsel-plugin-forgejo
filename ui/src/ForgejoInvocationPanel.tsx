import { Panel, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useState } from "react";
import type { InvocationResponse, Item } from "./api";

export interface ForgejoInvocationPanelProps {
  invocationId: string;
  agentName: string;
}

export function ForgejoInvocationPanel({ invocationId, agentName }: ForgejoInvocationPanelProps) {
  const { api } = usePluginContext();
  const [item, setItem] = useState<Item | null>(null);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get(`/invocations/${invocationId}/work`)
      .then((response) => {
        const invocResponse = response as InvocationResponse;
        setItem(invocResponse.item);
      })
      .catch((err) => {
        setError(err.message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [api, invocationId]);

  if (loading) {
    return <p className="forgejo-status">Loading...</p>;
  }

  if (error || !item) {
    return <p className="forgejo-error">Could not load invocation work</p>;
  }

  return (
    <Panel className="forgejo-invocation-panel">
      <div className="forgejo-item-link-container">
        <a href={item.url} target="_blank" rel="noreferrer" className="forgejo-item-link">
          {item.subject.kind}/{item.subject.number}
        </a>
        {item.subject.title && <span className="forgejo-item-title">{item.subject.title}</span>}
      </div>
      <div className="forgejo-agent-info">
        <p className="forgejo-agent-name">{agentName}</p>
      </div>
    </Panel>
  );
}
