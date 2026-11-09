import { useEffect, useState } from "react";
import { api, errorMessage } from "../lib/api";
import { useToast } from "../lib/toast";
import type { CloudflareAccount, Domain } from "../types";
import { Button, Field, Input, Modal, Select, Switch } from "./ui";

export function DomainAttachModal({
  open,
  instanceId,
  instanceName,
  onClose,
  onCreated,
}: {
  open: boolean;
  instanceId: string;
  instanceName: string;
  onClose: () => void;
  onCreated: (domain: Domain) => void;
}) {
  const toast = useToast();
  const [hostname, setHostname] = useState("");
  const [accounts, setAccounts] = useState<CloudflareAccount[]>([]);
  const [accountId, setAccountId] = useState("");
  const [manageCaddy, setManageCaddy] = useState(true);
  const [manageDns, setManageDns] = useState(false);
  const [proxied, setProxied] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string | null>(null);

  useEffect(() => {
    if (!open) return;
    setHostname("");
    setError(null);
    api
      .cloudflareAccounts()
      .then((result) => {
        setAccounts(result.items);
        setAccountId(result.items[0]?.id ?? "");
        setManageDns(result.items.length > 0 ? manageDns : false);
      })
      .catch(() => setAccounts([]));
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [open]);

  async function submit(event: React.FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(null);
    try {
      const result = await api.attachDomain(instanceId, {
        hostname: hostname.trim(),
        cloudflareAccountId: manageDns ? accountId : "",
        proxied,
        manageCaddy,
        manageDns,
      });
      toast.success("Domain attached", result.job ? `Integration job ${result.job.id} queued.` : "Recorded as unmanaged inventory.");
      onCreated(result.domain);
      onClose();
    } catch (err) {
      setError(errorMessage(err));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      open={open}
      onClose={onClose}
      title="Attach a domain"
      description={`Map a hostname to ${instanceName}. Existing unrelated Caddy routes and DNS records are never overwritten.`}
      size="md"
      footer={
        <>
          <Button onClick={onClose} disabled={busy}>Cancel</Button>
          <Button variant="primary" type="submit" form="attach-domain-form" loading={busy}>Attach domain</Button>
        </>
      }
    >
      <form id="attach-domain-form" className="stack" onSubmit={(event) => void submit(event)}>
        {error ? <div className="form-error">{error}</div> : null}
        <Field label="Hostname" required hint="Apex domains and subdomains are supported.">
          <Input value={hostname} onChange={(event) => setHostname(event.target.value)} placeholder="clinic-a.example.com" autoFocus required />
        </Field>
        <Switch
          checked={manageCaddy}
          onChange={setManageCaddy}
          label="Manage Caddy route"
          description="Creates a dedicated snippet, validates the full Caddyfile, backs up, and reloads."
        />
        <Switch
          checked={manageDns}
          onChange={setManageDns}
          label="Manage Cloudflare DNS"
          description="Creates or updates only a manager-owned A record."
          disabled={accounts.length === 0}
        />
        {manageDns ? (
          <>
            <Field label="Cloudflare account">
              <Select value={accountId} onChange={(event) => setAccountId(event.target.value)} required>
                <option value="">Select account</option>
                {accounts.map((account) => <option key={account.id} value={account.id}>{account.name}</option>)}
              </Select>
            </Field>
            <Switch checked={proxied} onChange={setProxied} label="Enable Cloudflare proxy" />
          </>
        ) : null}
        {accounts.length === 0 ? (
          <div className="notice notice-info">No Cloudflare account is configured. You can attach the domain with Caddy only, then add DNS later.</div>
        ) : null}
      </form>
    </Modal>
  );
}
