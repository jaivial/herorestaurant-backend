-- MiniMax-M3.1-Flash is the only model the product runs.
--
-- 1. Point every stored MiniMax model (translations, stock, Forky, bot) at
--    MiniMax-M3.1-Flash.
-- 2. Drop the bot router fallback: the fallback model is the same model, so
--    botLLMCallRouted must not retry another provider.
-- 3. Backfill the historical cost rows that were stored unpriced (MiniMax-M3)
--    or double-counted the prompt cache (DeepSeek rows came from an
--    OpenAI-compatible gateway, where prompt_tokens already includes the
--    cached tokens). Coordination id: ai_usage_logs_pricing_v2.

UPDATE restaurant_minimax_config
SET model = 'MiniMax-M3.1-Flash'
WHERE model <> 'MiniMax-M3.1-Flash';

UPDATE restaurant_bot_ai_config
SET primary_model = 'minimax/MiniMax-M3.1-Flash'
WHERE primary_model LIKE 'minimax/%' OR primary_model LIKE 'opencode-go/%';

UPDATE restaurant_bot_ai_config
SET fallback_model = ''
WHERE fallback_model LIKE 'minimax/%' OR fallback_model LIKE 'opencode-go/%';

-- MiniMax-M3 (legacy): $0.30 input, $1.20 output, $0.06 cache read per 1M.
UPDATE ai_usage_logs
SET cost_usd = ROUND((input_tokens * 0.30 + cache_read_tokens * 0.06 + output_tokens * 1.20) / 1000000, 8),
    priced = 1
WHERE model = 'MiniMax-M3' AND priced = 0;

-- DeepSeek (legacy, off-peak rates already applied at insert): the stored
-- input_tokens include the cached tokens, so charge only the uncached part at
-- the full rate and the cached part at the cache-read rate.
UPDATE ai_usage_logs
SET cost_usd = ROUND((
        (input_tokens - cache_read_tokens) *
            (CASE WHEN WEEKDAY(created_at) < 5 AND (HOUR(created_at) BETWEEN 1 AND 3 OR HOUR(created_at) BETWEEN 6 AND 9) THEN 0.30 ELSE 0.15 END) +
        cache_read_tokens *
            (CASE WHEN WEEKDAY(created_at) < 5 AND (HOUR(created_at) BETWEEN 1 AND 3 OR HOUR(created_at) BETWEEN 6 AND 9) THEN 0.006 ELSE 0.003 END) +
        output_tokens *
            (CASE WHEN WEEKDAY(created_at) < 5 AND (HOUR(created_at) BETWEEN 1 AND 3 OR HOUR(created_at) BETWEEN 6 AND 9) THEN 1.20 ELSE 0.60 END)
    ) / 1000000, 8)
WHERE model LIKE 'deepseek%' AND cache_read_tokens > 0;
