package sqlite

import (
	"database/sql"
	"fmt"
)

// TODO: Finish separating procedures from loader layer: make this a type to make
// unit tests more granular?
func addChannelEntry(tx *sql.Tx, channelName, packageName, csvName string, depth int) (int64, error) {
	addChannelEntry, err := tx.Prepare("insert into channel_entry(channel_name, package_name, operatorbundle_name, depth) values(?, ?, ?, ?)")
	if err != nil {
		return 0, err
	}
	defer addChannelEntry.Close()

	res, err := addChannelEntry.Exec(channelName, packageName, csvName, depth)
	if err != nil {
		return 0, fmt.Errorf("failed to insert channel_entry for channel (%s) and bundle (%s): %s", channelName, csvName, err)
	}
	currentID, err := res.LastInsertId()
	if err != nil {
		return 0, err
	}

	return currentID, err
}

func addReplaces(tx *sql.Tx, replacesID, entryID int64) error {
	addReplaces, err := tx.Prepare("update channel_entry set replaces = ? where entry_id = ?")
	if err != nil {
		return err
	}
	defer addReplaces.Close()

	_, err = addReplaces.Exec(replacesID, entryID)
	if err != nil {
		return fmt.Errorf("failed to update channel_entry to set replaces (%d) for entry (%d): %s", replacesID, entryID, err)
	}

	return nil
}

func addPackage(tx *sql.Tx, packageName string) error {
	addPackage, err := tx.Prepare("insert into package(name) values(?)")
	if err != nil {
		return err
	}
	defer addPackage.Close()

	_, err = addPackage.Exec(packageName)
	if err != nil {
		return fmt.Errorf("failed to insert package (%s): %s", packageName, err)
	}

	return nil
}

func addPackageIfNotExists(tx *sql.Tx, packageName string) error {
	addPackage, err := tx.Prepare("insert or replace into package(name) values(?)")
	if err != nil {
		return err
	}
	defer addPackage.Close()

	_, err = addPackage.Exec(packageName)
	if err != nil {
		return fmt.Errorf("failed to insert or replace package (%s): %s", packageName, err)
	}

	return nil
}

func addChannel(tx *sql.Tx, channelName, packageName, headCsvName string) error {
	addChannel, err := tx.Prepare("insert into channel(name, package_name, head_operatorbundle_name) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannel.Close()

	_, err = addChannel.Exec(channelName, packageName, headCsvName)
	if err != nil {
		return fmt.Errorf("failed to insert channel (%s) for package (%s) with head (%s) : %s", channelName, packageName, headCsvName, err)
	}

	return nil
}

func updateChannel(tx *sql.Tx, channelName, packageName, headCsvName string) error {
	updateChannel, err := tx.Prepare("update channel set head_operatorbundle_name = ? where name = ? and package_name = ?")
	if err != nil {
		return err
	}
	defer updateChannel.Close()

	_, err = updateChannel.Exec(channelName, packageName, headCsvName)
	if err != nil {
		return fmt.Errorf("failed to update channel (%s) for package (%s) with head (%s) : %s", channelName, packageName, headCsvName, err)

	}

	return nil
}

func addOrUpdateChannel(tx *sql.Tx, channelName, packageName, headCsvName string) error {
	addChannel, err := tx.Prepare("insert or replace into channel(name, package_name, head_operatorbundle_name) values(?, ?, ?)")
	if err != nil {
		return err
	}
	defer addChannel.Close()

	_, err = addChannel.Exec(channelName, packageName, headCsvName)
	if err != nil {
		return fmt.Errorf("failed to insert or update channel (%s) for package (%s) with head (%s) : %s", channelName, packageName, headCsvName, err)
	}

	return nil
}

func updateDefaultChannel(tx *sql.Tx, channelName, packageName string) error {
	updateDefaultChannel, err := tx.Prepare("update package set default_channel = ? where name = ?")
	if err != nil {
		return err
	}
	defer updateDefaultChannel.Close()

	_, err = updateDefaultChannel.Exec(channelName, packageName)
	if err != nil {
		return fmt.Errorf("failed to set default channel (%s) for package (%s): %s", channelName, packageName, err)
	}

	return nil
}

func truncChannelGraph(tx *sql.Tx, channelName, packageName string) error {
	truncChannelGraph, err := tx.Prepare("delete from channel_entry where channel_name = ? and package_name = ?")
	if err != nil {
		return err
	}
	defer truncChannelGraph.Close()

	_, err = truncChannelGraph.Exec(channelName, packageName)
	if err != nil {
		return fmt.Errorf("failed to delete channel entry for channel name (%s) and package (%s): %s", channelName, packageName, err)
	}

	return nil
}
