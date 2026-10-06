-- purge: a member can keep live and every to some channels. Empty is
-- everywhere, as before.
alter table purge_subs
	add column live_channels  bigint[] not null default '{}',
	add column every_channels bigint[] not null default '{}';

-- Each channel's parent: a thread's channel, a channel's category, 0 for
-- none. A scoped purge climbs it to find what the member picked.
alter table purge_channels add column parent_id bigint not null default 0;

-- List every archived thread once more, so threads read before parents
-- were kept get theirs. Only listing: nothing is read again.
update purge_channels set threads_listed_at = null;
