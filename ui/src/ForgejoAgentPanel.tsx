import { Panel, usePluginContext } from "@ainsel/plugin-sdk";
import { useEffect, useState } from "react";
import type { Item } from "./api";

export interface ForgejoAgentPanelProps {
  agentName: string;
}

interface AgentWorkResponse {
  items: Item[];
}

export function ForgejoAgentPanel({ agentName }: ForgejoAgentPanelProps) {
  const { api } = usePluginContext();
  const [items, setItems] = useState<Item[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    api
      .get<AgentWorkResponse>(`/agents/${agentName}/work`)
      .then((response) => {
        setItems(response.items || []);
      })
      .catch((err) => {
        setError(err.message);
      })
      .finally(() => {
        setLoading(false);
      });
  }, [api, agentName]);

  if (loading) {
    return <p className="forgejo-status">Loading...</p>;
  }

  if (error) {
    return <p className="forgejo-error">Could not load agent work</p>;
  }

  return (
    <Panel className="forgejo-agent-panel">
      {items.length === 0 ? (
        <p className="forgejo-no-work">No work for this agent</p>
      ) : (
        items.map((item) => (
          <div key={`${item.subject.number}`} className="forgejo-item">
            <a href={item.url} target="_blank" rel="noreferrer" className="forgejo-item-link">
              {item.subject.kind}/{item.subject.number}
            </a>
            {item.subject.title && <span className="forgejo-item-title">{item.subject.title}</span>}
          </div>
        ))
      )}
    </Panel>
  );
}
