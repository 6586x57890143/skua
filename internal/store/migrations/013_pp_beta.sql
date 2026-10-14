-- /pp is in closed beta: beside admins, only members holding beta_role may
-- play in a server. 0 is no role set, which leaves it to admins.
alter table pp_guild add column beta_role bigint not null default 0;
