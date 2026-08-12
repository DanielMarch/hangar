<?php
/**
 * fixtures.php — the dataset every recorded response is a view of.
 *
 * Deliberately small and hand-written: the corpus is a byte-comparison
 * target, so the data has to be reproducible exactly, and every value has
 * to be something HANGAR's own schema can also hold. Values are chosen to
 * exercise the cases that break byte-compatibility rather than to look
 * realistic:
 *
 *   - money large enough that IEEE-754 cannot represent it exactly
 *     (9007199254740993.01 > 2^53), so the shim's number conversion is
 *     actually tested rather than accidentally exact;
 *   - a NULL in every nullable column that a resource emits;
 *   - a multi-page collection (20 wallet journal rows against a page size
 *     of 15) so the pagination envelope has a real second page;
 *   - an empty collection, because `[]` and `null` are different answers
 *     and the shim must not conflate them.
 */

use Illuminate\Support\Facades\DB;

function seedFixtures(): void
{
    $now = '2026-08-01 12:00:00';

    DB::table('users')->insert([
        ['id' => 1, 'name' => 'Pilot One', 'active' => 1, 'admin' => 0,
            'last_login' => '2026-07-30 08:15:00', 'last_login_source' => '198.51.100.7',
            'main_character_id' => 90000001, 'created_at' => $now, 'updated_at' => $now],
        ['id' => 2, 'name' => 'Pilot Two', 'active' => 0, 'admin' => 0,
            'last_login' => null, 'last_login_source' => null,
            'main_character_id' => 90000002, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('character_infos')->insert([
        ['character_id' => 90000001, 'name' => 'Pilot One', 'description' => 'A description with "quotes" and a comma, plus ünïcode.',
            'birthday' => '2015-03-04 05:06:07', 'gender' => 'male', 'race_id' => 1, 'bloodline_id' => 3,
            'security_status' => 4.9482164, 'title' => null, 'created_at' => $now, 'updated_at' => $now],
        ['character_id' => 90000002, 'name' => 'Pilot Two', 'description' => null,
            'birthday' => '2016-04-05 06:07:08', 'gender' => 'female', 'race_id' => 2, 'bloodline_id' => 5,
            'security_status' => -1.5, 'title' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('character_affiliations')->insert([
        ['character_id' => 90000001, 'corporation_id' => 98000001, 'alliance_id' => 99000001,
            'faction_id' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('corporation_infos')->insert([
        ['corporation_id' => 98000001, 'name' => 'Test Corporation', 'ticker' => 'TSTC',
            'member_count' => 42, 'ceo_id' => 90000001, 'alliance_id' => 99000001,
            'description' => 'Corp description', 'tax_rate' => 0.1, 'date_founded' => '2014-01-02 03:04:05',
            'creator_id' => 90000001, 'url' => 'https://example.invalid/corp', 'faction_id' => null,
            'home_station_id' => 60003760, 'shares' => 1000, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('alliances')->insert([
        ['alliance_id' => 99000001, 'name' => 'Test Alliance', 'creator_id' => 90000001,
            'creator_corporation_id' => 98000001, 'ticker' => 'TSTA',
            'executor_corporation_id' => 98000001, 'date_founded' => '2014-02-03 04:05:06',
            'faction_id' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('character_wallet_balances')->insert([
        ['character_id' => 90000001, 'balance' => 9007199254740993.01, 'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('character_info_skills')->insert([
        ['character_id' => 90000001, 'total_sp' => 123456789, 'unallocated_sp' => 500000,
            'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── assets ───────────────────────────────────────────────────────────
    DB::table('character_assets')->insert([
        ['item_id' => 1000000000001, 'character_id' => 90000001, 'type_id' => 587, 'quantity' => 1,
            'location_id' => 60003760, 'location_flag' => 'Hangar', 'is_singleton' => 1,
            'is_blueprint_copy' => null, 'x' => null, 'y' => null, 'z' => null,
            'map_id' => null, 'map_name' => null, 'name' => 'My Rifter',
            'created_at' => $now, 'updated_at' => $now, 'location_type' => 'station'],
        ['item_id' => 1000000000002, 'character_id' => 90000001, 'type_id' => 34, 'quantity' => 12500,
            'location_id' => 1000000000001, 'location_flag' => 'Cargo', 'is_singleton' => 0,
            'is_blueprint_copy' => 0, 'x' => 1.5, 'y' => -2.25, 'z' => 0.0,
            'map_id' => 30000142, 'map_name' => 'Jita', 'name' => null,
            'created_at' => $now, 'updated_at' => $now, 'location_type' => 'other'],
    ]);
    DB::table('corporation_assets')->insert([
        ['item_id' => 2000000000001, 'corporation_id' => 98000001, 'type_id' => 34, 'quantity' => 999,
            'location_id' => 60003760, 'location_flag' => 'CorpSAG1', 'is_singleton' => 0,
            'is_blueprint_copy' => null, 'x' => null, 'y' => null, 'z' => null,
            'map_id' => null, 'map_name' => null, 'name' => null,
            'created_at' => $now, 'updated_at' => $now, 'location_type' => 'station'],
    ]);

    // ── contacts (+ labels, to exercise the pivot pluck) ─────────────────
    DB::table('character_labels')->insert([
        ['id' => 11, 'character_id' => 90000001, 'label_id' => 1, 'name' => 'Friendly', 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('character_contacts')->insert([
        ['id' => 21, 'character_id' => 90000001, 'contact_id' => 90000002, 'standing' => 10.0,
            'contact_type' => 'character', 'is_watched' => 1, 'is_blocked' => 0,
            'created_at' => $now, 'updated_at' => $now],
        ['id' => 22, 'character_id' => 90000001, 'contact_id' => 98000001, 'standing' => -5.0,
            'contact_type' => 'corporation', 'is_watched' => 0, 'is_blocked' => 1,
            'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('character_contact_character_label')->insert([
        ['character_contact_id' => 21, 'character_label_id' => 11],
    ]);

    DB::table('corporation_labels')->insert([
        ['id' => 31, 'corporation_id' => 98000001, 'label_id' => 2, 'name' => 'Blue', 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('corporation_contacts')->insert([
        ['id' => 41, 'corporation_id' => 98000001, 'contact_id' => 90000002, 'standing' => 5.0,
            'contact_type' => 'character', 'is_watched' => null,
            'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('corporation_contact_corporation_label')->insert([
        ['corporation_contact_id' => 41, 'corporation_label_id' => 31],
    ]);

    DB::table('alliance_contacts')->insert([
        ['id' => 51, 'alliance_id' => 99000001, 'contact_id' => 98000001, 'standing' => 7.5,
            'contact_type' => 'corporation', 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── corporation history ──────────────────────────────────────────────
    DB::table('character_corporation_histories')->insert([
        ['character_id' => 90000001, 'record_id' => 1, 'corporation_id' => 98000001,
            'is_deleted' => null, 'start_date' => '2020-01-01 00:00:00', 'created_at' => $now, 'updated_at' => $now],
        ['character_id' => 90000001, 'record_id' => 2, 'corporation_id' => 98000002,
            'is_deleted' => 1, 'start_date' => '2018-06-15 10:30:00', 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── industry ─────────────────────────────────────────────────────────
    foreach ([['character_industry_jobs', 'character_id', 90000001, 5001, 'station_id'],
              ['corporation_industry_jobs', 'corporation_id', 98000001, 5002, 'location_id']] as [$table, $ownerCol, $ownerId, $jobId, $placeCol]) {
        DB::table($table)->insert([[
            $ownerCol => $ownerId, 'job_id' => $jobId, 'installer_id' => 90000001,
            'facility_id' => 60003760, $placeCol => 60003760, 'activity_id' => 1,
            'blueprint_id' => 1000000000003, 'blueprint_type_id' => 587,
            'blueprint_location_id' => 60003760, 'output_location_id' => 60003760,
            'runs' => 10, 'cost' => 1234567.89, 'licensed_runs' => null, 'probability' => null,
            'product_type_id' => 34, 'status' => 'active', 'duration' => 3600,
            'start_date' => '2026-07-31 00:00:00', 'end_date' => '2026-07-31 01:00:00',
            'pause_date' => null, 'completed_date' => null, 'completed_character_id' => null,
            'successful_runs' => null, 'created_at' => $now, 'updated_at' => $now,
        ]]);
    }

    // ── jump clones ──────────────────────────────────────────────────────
    DB::table('character_jump_clones')->insert([
        ['character_id' => 90000001, 'jump_clone_id' => 6001, 'name' => 'Home', 'location_id' => 60003760,
            'location_type' => 'station', 'implants' => '[]', 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── mail ─────────────────────────────────────────────────────────────
    DB::table('mail_headers')->insert([
        ['mail_id' => 7001, 'subject' => 'Re: fleet', 'from' => 90000002, 'timestamp' => '2026-07-29 18:00:00',
            'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('mail_bodies')->insert([
        ['mail_id' => 7001, 'body' => '<p>See you at the gate.</p>', 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('mail_recipients')->insert([
        ['mail_id' => 7001, 'recipient_id' => 90000001, 'recipient_type' => 'character', 'is_read' => 1, 'labels' => json_encode([1])],
    ]);

    // ── notifications ────────────────────────────────────────────────────
    DB::table('character_notifications')->insert([
        ['id' => 8001, 'character_id' => 90000001, 'notification_id' => 900001, 'type' => 'StructureUnderAttack',
            'sender_id' => 98000001, 'sender_type' => 'corporation', 'timestamp' => '2026-07-28 09:00:00',
            'is_read' => 0, 'text' => "solarsystemID: 30000142\nstructureID: 1000000000004\n",
            'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── market orders ────────────────────────────────────────────────────
    DB::table('character_orders')->insert([
        ['character_id' => 90000001, 'order_id' => 9001, 'type_id' => 34, 'region_id' => 10000002,
            'location_id' => 60003760, 'range' => 'station', 'is_buy_order' => 1, 'price' => 5.55,
            'volume_total' => 1000000, 'volume_remain' => 999999, 'issued' => '2026-07-27 12:00:00',
            'state' => 'open', 'min_volume' => 1, 'duration' => 90,
            'is_corporation' => 0, 'escrow' => 5550000.0, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('corporation_orders')->insert([
        ['corporation_id' => 98000001, 'order_id' => 9002, 'type_id' => 587, 'region_id' => 10000002,
            'location_id' => 60003760, 'range' => 'region', 'is_buy_order' => 0, 'price' => 10000000.5,
            'volume_total' => 5, 'volume_remain' => 5, 'issued' => '2026-07-26 12:00:00',
            'state' => 'open', 'min_volume' => 1, 'wallet_division' => 1, 'duration' => 30,
            'escrow' => null, 'issued_by' => 90000001, 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── skills ───────────────────────────────────────────────────────────
    DB::table('character_skills')->insert([
        ['character_id' => 90000001, 'skill_id' => 587, 'skillpoints_in_skill' => 256000,
            'trained_skill_level' => 5, 'active_skill_level' => 5, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('character_skill_queues')->insert([
        ['character_id' => 90000001, 'skill_id' => 34, 'finish_date' => '2026-09-01 00:00:00',
            'start_date' => '2026-08-01 00:00:00', 'finished_level' => 4, 'queue_position' => 0,
            'training_start_sp' => 1000, 'level_end_sp' => 90510, 'level_start_sp' => 16000,
            'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── wallets ──────────────────────────────────────────────────────────
    // 20 journal rows against a page size of 15: a real second page, so the
    // pagination envelope is recorded with non-null prev/next rather than
    // the degenerate single-page shape.
    $journal = [];
    for ($i = 1; $i <= 20; $i++) {
        $journal[] = [
            'character_id' => 90000001, 'id' => 10000 + $i, 'date' => sprintf('2026-07-%02d 00:00:00', $i),
            'ref_type' => 'player_donation', 'first_party_id' => 90000002, 'second_party_id' => 90000001,
            'amount' => 9007199254740993.01, 'balance' => 9007199254740993.01 + $i,
            'reason' => $i === 1 ? null : 'reason ' . $i, 'tax_receiver_id' => null, 'tax' => null,
            'context_id' => null, 'context_id_type' => null, 'description' => 'A donation',
            'created_at' => $now, 'updated_at' => $now,
        ];
    }
    DB::table('character_wallet_journals')->insert($journal);

    DB::table('character_wallet_transactions')->insert([
        ['character_id' => 90000001, 'transaction_id' => 11001, 'date' => '2026-07-20 15:00:00',
            'type_id' => 34, 'location_id' => 60003760, 'unit_price' => 5.55, 'quantity' => 1000,
            'client_id' => 90000002, 'is_buy' => 1, 'is_personal' => 1, 'journal_ref_id' => 10001,
            'created_at' => $now, 'updated_at' => $now],
    ]);

    DB::table('corporation_wallet_journals')->insert([
        ['corporation_id' => 98000001, 'division' => 1, 'id' => 12001, 'date' => '2026-07-19 10:00:00',
            'ref_type' => 'bounty_prizes', 'first_party_id' => null, 'second_party_id' => 98000001,
            'amount' => 1000000.25, 'balance' => 50000000.75, 'reason' => null,
            'tax_receiver_id' => null, 'tax' => null, 'context_id' => null, 'context_id_type' => null,
            'description' => 'Bounty', 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('corporation_wallet_transactions')->insert([
        ['corporation_id' => 98000001, 'division' => 1, 'transaction_id' => 13001, 'date' => '2026-07-18 09:00:00',
            'type_id' => 587, 'location_id' => 60003760, 'unit_price' => 10000000.5, 'quantity' => 2,
            'client_id' => 90000002, 'is_buy' => 0, 'journal_ref_id' => 12001,
            'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── member tracking ──────────────────────────────────────────────────
    DB::table('corporation_member_trackings')->insert([
        ['corporation_id' => 98000001, 'character_id' => 90000001, 'start_date' => '2020-01-01 00:00:00',
            'base_id' => null, 'logon_date' => '2026-07-30 08:00:00', 'logoff_date' => '2026-07-30 10:00:00',
            'location_id' => 60003760, 'ship_type_id' => 587, 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── contracts ────────────────────────────────────────────────────────
    DB::table('contract_details')->insert([
        ['contract_id' => 14001, 'acceptor_id' => 0, 'assignee_id' => 90000002, 'availability' => 'personal',
            'buyout' => null, 'collateral' => 0.0, 'date_accepted' => null, 'date_completed' => null,
            'date_expired' => '2026-08-30 00:00:00', 'date_issued' => '2026-07-25 00:00:00',
            'days_to_complete' => 0, 'end_location_id' => 60003760, 'for_corporation' => 0,
            'issuer_corporation_id' => 98000001, 'issuer_id' => 90000001, 'price' => 9007199254740993.01,
            'reward' => 0.0, 'start_location_id' => 60003760, 'status' => 'outstanding',
            'title' => 'A contract', 'type' => 'item_exchange', 'volume' => 27289.0,
            'start_location_type' => \Seat\Eveapi\Models\Universe\UniverseStation::class,
            'end_location_type' => \Seat\Eveapi\Models\Universe\UniverseStation::class],
    ]);
    DB::table('character_contracts')->insert([
        ['character_id' => 90000001, 'contract_id' => 14001, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('corporation_contracts')->insert([
        ['corporation_id' => 98000001, 'contract_id' => 14001, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('contract_items')->insert([
        ['contract_id' => 14001, 'record_id' => 1, 'type_id' => 34, 'quantity' => 100,
            'is_included' => 1, 'is_singleton' => 0, 'raw_quantity' => null],
    ]);

    // ── structures ───────────────────────────────────────────────────────
    DB::table('corporation_structures')->insert([
        ['corporation_id' => 98000001, 'structure_id' => 1000000000004, 'type_id' => 587,
            'system_id' => 30000142, 'profile_id' => 1, 'fuel_expires' => null,
            'state_timer_start' => null, 'state_timer_end' => null, 'unanchors_at' => null,
            'state' => 'shield_vulnerable', 'reinforce_weekday' => null, 'reinforce_hour' => 18,
            'next_reinforce_weekday' => null, 'next_reinforce_hour' => null,
            'next_reinforce_apply' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── killmails ────────────────────────────────────────────────────────
    DB::table('killmails')->insert([
        ['killmail_id' => 15001, 'killmail_hash' => 'abc123def456', 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('killmail_details')->insert([
        ['killmail_id' => 15001, 'killmail_time' => '2026-07-15 20:00:00', 'solar_system_id' => 30000142,
            'moon_id' => null, 'war_id' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('killmail_victims')->insert([
        ['killmail_id' => 15001, 'character_id' => 90000001, 'corporation_id' => 98000001,
            'alliance_id' => 99000001, 'faction_id' => null, 'damage_taken' => 4567,
            'ship_type_id' => 587, 'x' => null, 'y' => null, 'z' => null, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('killmail_attackers')->insert([
        ['attacker_hash' => 'att-1', 'killmail_id' => 15001, 'character_id' => 90000002, 'corporation_id' => 98000002,
            'alliance_id' => null, 'faction_id' => null, 'damage_done' => 4567, 'final_blow' => 1,
            'security_status' => -2.5, 'ship_type_id' => 587, 'weapon_type_id' => 2456,
            'created_at' => $now, 'updated_at' => $now],
    ]);

    // ── squads ───────────────────────────────────────────────────────────
    DB::table('squads')->insert([
        ['id' => 1, 'name' => 'Alpha Squad', 'description' => 'The first squad', 'logo' => null,
            'type' => 'manual', 'filters' => null, 'is_classified' => 0, 'created_at' => $now, 'updated_at' => $now],
        ['id' => 2, 'name' => 'Bravo Squad', 'description' => 'The second squad', 'logo' => null,
            'type' => 'auto', 'filters' => null, 'is_classified' => 0, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('squad_member')->insert([
        ['squad_id' => 1, 'user_id' => 1, 'created_at' => $now, 'updated_at' => $now],
    ]);
    DB::table('squad_moderator')->insert([
        ['squad_id' => 1, 'user_id' => 1],
    ]);
}
