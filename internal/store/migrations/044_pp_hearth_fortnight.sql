-- The hearth's usual day now reads a fortnight (Usual), and a server kept
-- only a week. Each day's raw pp is the sum of its members' labour rows'
-- voice and chat, so the fortnight is taken back from the ledger, oldest
-- first, the open day aside. A server whose ledger holds fewer days than
-- its history (a history older than its ledger) keeps what it has.
update pp_guild g set history = h.days
from (
	select guild_id, array_agg(raw order by day) as days
	from (
		select guild_id, day, sum(inputs[1] + inputs[2]) as raw,
			row_number() over (partition by guild_id order by day desc) as back
		from pp_ledger
		where reason = 'labour'
		group by guild_id, day
	) d
	where back <= 14 and raw > 0
	group by guild_id
) h
where g.guild_id = h.guild_id and g.day > 0 and cardinality(h.days) > cardinality(g.history);
