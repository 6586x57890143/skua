-- The channel a member's chirograph or round robin was last put up in,
-- which hears when it's done. Its own column, so the pact's text reads
-- the same to a binary from before it.
alter table pp_member add column pact_channel bigint not null default 0;
