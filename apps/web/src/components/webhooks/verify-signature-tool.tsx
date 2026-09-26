'use client';

import { useState } from 'react';
import { api } from '@/lib/api';
import { WEBHOOK_VERIFICATION_SNIPPETS } from '@/lib/webhook-verification-snippets';
import { Card, CardContent, CardHeader, CardTitle } from '@/components/ui/card';
import { Button } from '@/components/ui/button';
import { Input } from '@/components/ui/input';
import { Textarea } from '@/components/ui/textarea';
import { Badge } from '@/components/ui/badge';
import { CheckCircle2, XCircle, Copy, Check } from 'lucide-react';

function extractHeader(rawHeaders: string, name: string): string {
  const pattern = new RegExp(`^${name}\\s*:\\s*(.+)$`, 'im');
  const match = rawHeaders.match(pattern);
  return match ? match[1].trim() : '';
}

const REASON_LABELS: Record<string, string> = {
  invalid_timestamp: 'The timestamp is not a valid Unix timestamp.',
  stale_timestamp: 'The timestamp is more than 5 minutes old (or in the future) — rejected to prevent replay attacks.',
  signature_mismatch: 'The computed signature does not match the provided signature. Check the secret, and that the body is the exact raw bytes that were signed.',
};

export function VerifySignatureTool() {
  const [rawHeaders, setRawHeaders] = useState('');
  const [body, setBody] = useState('');
  const [secret, setSecret] = useState('');
  const [verifying, setVerifying] = useState(false);
  const [result, setResult] = useState<{ valid: boolean; reason: string | null } | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [copiedLang, setCopiedLang] = useState<string | null>(null);

  const timestamp = extractHeader(rawHeaders, 'X-Fluxa-Timestamp');
  const signature = extractHeader(rawHeaders, 'X-Fluxa-Signature');

  const handleVerify = async (e: React.FormEvent) => {
    e.preventDefault();
    setVerifying(true);
    setError(null);
    setResult(null);
    try {
      const res = await api.verifyWebhookSignature({ secret, timestamp, body, signature });
      setResult(res);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Verification request failed');
    } finally {
      setVerifying(false);
    }
  };

  const handleCopy = async (lang: string, code: string) => {
    try {
      await navigator.clipboard.writeText(code);
      setCopiedLang(lang);
      setTimeout(() => setCopiedLang((current) => (current === lang ? null : current)), 2000);
    } catch {
      // Clipboard access can be denied by the browser; nothing to recover from here.
    }
  };

  return (
    <Card className="max-w-3xl">
      <CardHeader>
        <CardTitle>Verify Signature</CardTitle>
      </CardHeader>
      <CardContent className="flex flex-col gap-5">
        <p className="text-sm text-muted-foreground">
          Paste a delivery&apos;s raw request headers and body to check whether it verifies
          against your webhook secret — useful for debugging your own verification code
          without waiting for a real event.
        </p>

        <form onSubmit={handleVerify} className="flex flex-col gap-4">
          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium text-foreground">Request headers</label>
            <Textarea
              rows={4}
              value={rawHeaders}
              onChange={(e) => setRawHeaders(e.target.value)}
              placeholder={'X-Fluxa-Signature: sha256=...\nX-Fluxa-Timestamp: 1700000000'}
              className="font-mono text-xs"
            />
            <div className="flex gap-4 text-xs text-muted-foreground">
              <span>Timestamp: {timestamp || <em>not found</em>}</span>
              <span>Signature: {signature ? `${signature.slice(0, 20)}…` : <em>not found</em>}</span>
            </div>
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium text-foreground">Request body</label>
            <Textarea
              rows={6}
              value={body}
              onChange={(e) => setBody(e.target.value)}
              placeholder="Paste the exact raw body, unmodified"
              className="font-mono text-xs"
            />
          </div>

          <div className="flex flex-col gap-1.5">
            <label className="text-sm font-medium text-foreground">Webhook secret</label>
            <Input
              type="password"
              value={secret}
              onChange={(e) => setSecret(e.target.value)}
              placeholder="whsec_..."
              autoComplete="off"
            />
          </div>

          <div>
            <Button type="submit" isLoading={verifying} disabled={!secret || !body || !timestamp || !signature}>
              Verify
            </Button>
          </div>
        </form>

        {error && (
          <div className="rounded-lg border border-danger/30 bg-danger-subtle px-4 py-3 text-sm text-danger">
            {error}
          </div>
        )}

        {result && (
          <div
            className={`flex items-start gap-3 rounded-lg border px-4 py-3 text-sm ${
              result.valid
                ? 'border-success/30 bg-success-subtle text-success'
                : 'border-danger/30 bg-danger-subtle text-danger'
            }`}
          >
            {result.valid ? (
              <CheckCircle2 className="h-5 w-5 shrink-0" />
            ) : (
              <XCircle className="h-5 w-5 shrink-0" />
            )}
            <div className="flex flex-col gap-1">
              <div className="flex items-center gap-2 font-semibold">
                <Badge variant={result.valid ? 'success' : 'danger'}>
                  {result.valid ? 'Valid' : 'Invalid'}
                </Badge>
                {result.reason && <span>{result.reason}</span>}
              </div>
              {result.reason && REASON_LABELS[result.reason] && (
                <p className="text-muted-foreground">{REASON_LABELS[result.reason]}</p>
              )}
            </div>
          </div>
        )}

        <div className="flex flex-col gap-3 border-t border-border pt-5">
          <h3 className="text-sm font-semibold text-foreground">Reference implementations</h3>
          <div className="grid grid-cols-1 gap-3 sm:grid-cols-2">
            {Object.entries(WEBHOOK_VERIFICATION_SNIPPETS).map(([lang, snippet]) => (
              <Button
                key={lang}
                type="button"
                variant="secondary"
                size="sm"
                onClick={() => handleCopy(lang, snippet.code)}
                className="justify-between"
              >
                {snippet.label}
                {copiedLang === lang ? (
                  <Check className="h-3.5 w-3.5" />
                ) : (
                  <Copy className="h-3.5 w-3.5" />
                )}
              </Button>
            ))}
          </div>
        </div>
      </CardContent>
    </Card>
  );
}
